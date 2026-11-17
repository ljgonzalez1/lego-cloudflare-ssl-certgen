#include "core.hpp"

#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <cerrno>
#include <cstring>
#include <ctime>
#include <stdexcept>
#include <string>
#include <string_view>
#include <system_error>

// Run one lego request with a deadline. lego retries a 429 response after the
// full Retry-After delay, which can be many hours, so the wrapper stops it once
// Let's Encrypt reports a rate limit or the deadline passes.
namespace certgen {
namespace {
constexpr std::string_view rate_limit_marker = "urn:ietf:params:acme:error:rateLimited";
constexpr int grace_seconds = 10;
volatile sig_atomic_t forwarded_signal = 0;

void remember_signal(int signal_number) {
    forwarded_signal = signal_number;
}

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

void stop(pid_t child, int signal_number) {
    if (kill(-child, signal_number) != 0) {
        kill(child, signal_number);
    }
}
}

int supervise(const char* limit_text, char* command[]) {
    const auto limit = read_number(limit_text);
    const int handled[] = {SIGINT, SIGTERM, SIGHUP};
    sigset_t blocked;
    sigset_t previous;
    sigemptyset(&blocked);
    sigaddset(&blocked, SIGCHLD);
    for (const auto signal_number : handled) {
        sigaddset(&blocked, signal_number);
    }
    if (sigprocmask(SIG_BLOCK, &blocked, &previous) != 0) {
        os_error("cannot block signals");
    }
    struct sigaction action {};
    action.sa_handler = remember_signal;
    sigemptyset(&action.sa_mask);
    for (const auto signal_number : handled) {
        sigaction(signal_number, &action, nullptr);
    }
    struct sigaction child_action {};
    child_action.sa_handler = [](int) {};
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
        for (const auto signal_number : handled) {
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

    sigset_t waiting = previous;
    sigdelset(&waiting, SIGCHLD);
    for (const auto signal_number : handled) {
        sigdelset(&waiting, signal_number);
    }

    const double started = now();
    double kill_at = 0;
    bool timed_out = false;
    bool rate_limited = false;
    bool pipe_open = true;
    std::string line;
    char buffer[4096];
    // 124 matches timeout(1). 75 is EX_TEMPFAIL: retry after the rate limit resets.
    const auto result = [&](int status) {
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
    };

    for (;;) {
        int status = 0;
        const auto finished = waitpid(child, &status, WNOHANG);
        if (finished == child && !pipe_open) {
            return result(status);
        }
        if (finished == child) {
            // Drain anything lego wrote before it exited.
            pipe_open = false;
            for (;;) {
                const auto count = read(stderr_pipe[0], buffer, sizeof(buffer));
                if (count <= 0) {
                    break;
                }
                write_all(STDERR_FILENO, buffer, static_cast<std::size_t>(count));
                line.append(buffer, static_cast<std::size_t>(count));
                if (line.find(rate_limit_marker) != std::string::npos) {
                    rate_limited = true;
                }
            }
            close(stderr_pipe[0]);
            return result(status);
        }

        if (forwarded_signal != 0) {
            stop(child, forwarded_signal);
            forwarded_signal = 0;
        }

        const double current = now();
        if (!timed_out && !rate_limited && limit > 0 &&
            current - started >= static_cast<double>(limit)) {
            timed_out = true;
            stop(child, SIGTERM);
            kill_at = current + grace_seconds;
        }
        if (kill_at > 0 && current >= kill_at) {
            stop(child, SIGKILL);
            kill_at = 0;
        }

        double wait_for = 1.0;
        if (!timed_out && !rate_limited && limit > 0) {
            wait_for = static_cast<double>(limit) - (current - started);
        }
        if (wait_for > 1.0) {
            wait_for = 1.0;
        }
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
            const auto count = read(stderr_pipe[0], buffer, sizeof(buffer));
            if (count <= 0) {
                pipe_open = false;
                close(stderr_pipe[0]);
                continue;
            }
            write_all(STDERR_FILENO, buffer, static_cast<std::size_t>(count));
            line.append(buffer, static_cast<std::size_t>(count));
            if (!rate_limited && line.find(rate_limit_marker) != std::string::npos) {
                rate_limited = true;
                stop(child, SIGTERM);
                kill_at = now() + grace_seconds;
            }
            const auto newline = line.rfind('\n');
            if (newline != std::string::npos) {
                line.erase(0, newline + 1);
            } else if (line.size() > 65536) {
                line.erase(0, line.size() - rate_limit_marker.size());
            }
        }
    }
}
}
