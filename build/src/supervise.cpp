#include "core.hpp"

#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <cerrno>
#include <cstdint>
#include <cstring>
#include <ctime>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <system_error>

// Run one lego request under a deadline. lego never gives up on its own in some
// cases: a 429 response makes it sleep for the whole Retry-After delay, which
// can be a day. The wrapper kills lego with SIGKILL when Let's Encrypt reports
// a rate limit or when the deadline passes, so the next certificate can run.
namespace certgen {
namespace {
// Time lego may need besides DNS propagation: account registration, the new
// order, finalization and the certificate download (lego --cert.timeout is 30 s).
constexpr std::uint64_t order_seconds = 120;
// Per name: create and delete the TXT record through the Cloudflare API and
// let Let's Encrypt check it. lego then waits up to PROPAGATION_SECONDS.
constexpr std::uint64_t name_seconds = 60;
constexpr std::uint64_t maximum_seconds = 2 * 60 * 60;
constexpr std::string_view rate_limit_marker = "urn:ietf:params:acme:error:rateLimited";
volatile sig_atomic_t forwarded_signal = 0;

void remember_signal(int signal_number) {
    forwarded_signal = signal_number;
}

void ignore_signal(int) {}

[[noreturn]] void os_error(const char* message) {
    throw std::system_error(errno, std::generic_category(), message);
}

double now() {
    timespec clock{};
    clock_gettime(CLOCK_MONOTONIC, &clock);
    return static_cast<double>(clock.tv_sec) + static_cast<double>(clock.tv_nsec) / 1e9;
}

void write_all(int fd, const char* data, std::size_t size) {
    while (size > 0) {
        const auto written = write(fd, data, size);
        if (written < 0) {
            if (errno == EINTR) {
                continue;
            }
            return;
        }
        data += written;
        size -= static_cast<std::size_t>(written);
    }
}

// lego runs in its own process group, so this also reaches anything it started.
void signal_lego(pid_t child, int signal_number) {
    if (kill(-child, signal_number) != 0) {
        kill(child, signal_number);
    }
}
}

std::uint64_t time_limit(std::uint64_t names, std::uint64_t propagation) {
    if (names < 1 || names > 100) {
        throw std::runtime_error("name count must be between 1 and 100");
    }
    if (propagation > 2147483647) {
        throw std::runtime_error("propagation seconds are out of range");
    }
    const auto estimate = order_seconds + names * (propagation + name_seconds);
    // Add 50% headroom, rounded up, and never wait more than two hours.
    const auto limit = (estimate * 3 + 1) / 2;
    return limit < maximum_seconds ? limit : maximum_seconds;
}

void write_time_limit(const char* names, const char* propagation) {
    std::cout << time_limit(read_number(names), read_number(propagation)) << '\n';
}

int supervise(const char* limit_text, char* command[]) {
    const auto limit = read_number(limit_text);
    if (limit < 1) {
        throw std::runtime_error("time limit must be at least one second");
    }
    const int forwarded[] = {SIGINT, SIGTERM, SIGHUP};
    sigset_t blocked;
    sigset_t previous;
    sigemptyset(&blocked);
    sigaddset(&blocked, SIGCHLD);
    for (const auto signal_number : forwarded) {
        sigaddset(&blocked, signal_number);
    }
    if (sigprocmask(SIG_BLOCK, &blocked, &previous) != 0) {
        os_error("cannot block signals");
    }
    struct sigaction action {};
    action.sa_handler = remember_signal;
    sigemptyset(&action.sa_mask);
    for (const auto signal_number : forwarded) {
        sigaction(signal_number, &action, nullptr);
    }
    struct sigaction child_action {};
    child_action.sa_handler = ignore_signal;
    sigemptyset(&child_action.sa_mask);
    sigaction(SIGCHLD, &child_action, nullptr);

    int stderr_pipe[2];
    if (pipe(stderr_pipe) != 0) {
        os_error("cannot create a pipe");
    }
    const pid_t child = fork();
    if (child < 0) {
        os_error("cannot start lego");
    }
    if (child == 0) {
        setpgid(0, 0);
        for (const auto signal_number : forwarded) {
            signal(signal_number, SIG_DFL);
        }
        sigprocmask(SIG_SETMASK, &previous, nullptr);
        dup2(stderr_pipe[1], STDERR_FILENO);
        close(stderr_pipe[0]);
        close(stderr_pipe[1]);
        execvp(command[0], command);
        const std::string message = std::string(command[0]) + ": " + std::strerror(errno) + '\n';
        write_all(STDERR_FILENO, message.data(), message.size());
        _exit(127);
    }
    setpgid(child, child);
    close(stderr_pipe[1]);
    fcntl(stderr_pipe[0], F_SETFD, FD_CLOEXEC);
    fcntl(stderr_pipe[0], F_SETFL, fcntl(stderr_pipe[0], F_GETFL) | O_NONBLOCK);

    sigset_t waiting = previous;
    sigdelset(&waiting, SIGCHLD);
    for (const auto signal_number : forwarded) {
        sigdelset(&waiting, signal_number);
    }

    const double deadline = now() + static_cast<double>(limit);
    bool timed_out = false;
    bool rate_limited = false;
    bool killed = false;
    bool pipe_open = true;
    std::string line;
    char buffer[4096];

    // Copy lego's stderr through unchanged and look for a rate-limit error.
    // Returns false when no data is waiting or the pipe has closed.
    const auto relay = [&]() {
        const auto count = read(stderr_pipe[0], buffer, sizeof(buffer));
        if (count < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)) {
            return false;
        }
        if (count <= 0) {
            pipe_open = false;
            close(stderr_pipe[0]);
            return false;
        }
        write_all(STDERR_FILENO, buffer, static_cast<std::size_t>(count));
        line.append(buffer, static_cast<std::size_t>(count));
        if (line.find(rate_limit_marker) != std::string::npos) {
            rate_limited = true;
        }
        const auto newline = line.rfind('\n');
        if (newline != std::string::npos) {
            line.erase(0, newline + 1);
        } else if (line.size() > 65536) {
            line.erase(0, line.size() - rate_limit_marker.size());
        }
        return true;
    };

    for (;;) {
        int status = 0;
        if (waitpid(child, &status, WNOHANG) == child) {
            // Drain what lego wrote before it exited without waiting for
            // any process that might still hold the pipe open.
            while (pipe_open && relay()) {
            }
            if (pipe_open) {
                close(stderr_pipe[0]);
            }
            // 124 matches timeout(1). 75 is EX_TEMPFAIL: retry after the limit resets.
            if (timed_out) {
                return 124;
            }
            if (rate_limited) {
                return 75;
            }
            if (WIFEXITED(status)) {
                return WEXITSTATUS(status);
            }
            return 128 + WTERMSIG(status);
        }

        if (forwarded_signal != 0) {
            signal_lego(child, forwarded_signal);
            forwarded_signal = 0;
        }
        const double remaining = deadline - now();
        if (!killed && remaining <= 0) {
            timed_out = true;
        }
        if (!killed && (timed_out || rate_limited)) {
            signal_lego(child, SIGKILL);
            killed = true;
        }

        double wait_for = killed ? 0.1 : (remaining < 1.0 ? remaining : 1.0);
        if (wait_for < 0.01) {
            wait_for = 0.01;
        }
        timespec timeout{};
        timeout.tv_sec = static_cast<time_t>(wait_for);
        timeout.tv_nsec = static_cast<long>((wait_for - static_cast<double>(timeout.tv_sec)) * 1e9);
        pollfd watched{stderr_pipe[0], POLLIN, 0};
        const auto ready = ppoll(pipe_open ? &watched : nullptr, pipe_open ? 1 : 0, &timeout, &waiting);
        if (ready < 0 && errno != EINTR) {
            os_error("cannot wait for lego");
        }
        if (ready > 0 && pipe_open) {
            relay();
        }
    }
}
}
