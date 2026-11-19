#include "core.hpp"
#include "lego_log.hpp"
#include "lego_view.hpp"

#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>
#include <cstdint>
#include <cstring>
#include <ctime>
#include <iostream>
#include <iterator>
#include <memory>
#include <stdexcept>
#include <string>
#include <string_view>
#include <system_error>

// Run one certificate request under its own deadline. A confirmed failure
// shortens the deadline so a long retry or a stalled exit cannot block later groups.
namespace certgen {
namespace {
// Time lego may need besides DNS propagation: account registration, the new
// order, finalization and the certificate download (lego --cert.timeout is 30 s).
constexpr std::uint64_t order_seconds = 120;
// Per name: create and delete the TXT record through the Cloudflare API and
// let Let's Encrypt check it. lego then waits up to PROPAGATION_SECONDS.
constexpr std::uint64_t name_seconds = 60;
constexpr std::uint64_t maximum_seconds = 2 * 60 * 60;
constexpr double final_log_grace = 0.25;
constexpr std::size_t max_log_line = 16384;
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

bool nonempty_file(const std::string& path) {
    struct stat info {};
    return stat(path.c_str(), &info) == 0 && S_ISREG(info.st_mode) && info.st_size > 0;
}
}

std::uint64_t time_limit(std::uint64_t names, std::uint64_t propagation) {
    if (names < 1 || names > max_certificate_names) {
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

// certgen-core supervise [--log FILE] [--view NAME,...] [--certificate STEM]
//                       [--group GROUP] SECONDS COMMAND...
// --log appends everything lego prints to FILE.
// --view shows the live view on stdout instead of lego's own output.
// --certificate requires STEM.crt and STEM.key before reporting success.
int supervise(int argc, char* argv[]) {
    const char* log_path = nullptr;
    const char* view_names = nullptr;
    const char* certificate = nullptr;
    const char* group = nullptr;
    int argument = 0;
    constexpr std::string_view options[] = {"--log", "--view", "--certificate", "--group"};
    const char** values[] = {&log_path, &view_names, &certificate, &group};
    while (argument + 1 < argc) {
        const auto option = std::find(std::begin(options), std::end(options), argv[argument]);
        if (option == std::end(options)) {
            break;
        }
        *values[option - std::begin(options)] = argv[argument + 1];
        argument += 2;
    }
    if (argc - argument < 2) {
        throw std::runtime_error("expected SECONDS COMMAND...");
    }
    const auto limit = read_number(argv[argument]);
    char** command = argv + argument + 1;
    if (limit < 1) {
        throw std::runtime_error("time limit must be at least one second");
    }
    int log_fd = -1;
    if (log_path) {
        log_fd = open(log_path, O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC, 0600);
        if (log_fd < 0) {
            os_error("cannot open the lego log");
        }
    }
    std::unique_ptr<LegoView> view;
    if (view_names && *view_names == '\0') {
        view_names = nullptr;
    }
    if (view_names) {
        view = std::make_unique<LegoView>(view_names, limit, STDOUT_FILENO);
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
        // lego logs to stderr and prints advice to stdout; both go to the log file.
        dup2(stderr_pipe[1], STDOUT_FILENO);
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
    if (view) {
        view->start(now());
    }
    bool timed_out = false;
    bool killed = false;
    bool pipe_open = true;
    double stop_at = deadline;
    LegoLog log;
    std::string pending;
    char buffer[4096];

    const auto observe = [&](std::string_view text) {
        const LegoLine line(text);
        const bool was_failed = log.failed();
        log.observe(line);
        if (!was_failed && log.failed()) {
            // Give lego a moment to print the remainder of a multiline error and exit normally.
            stop_at = std::min(deadline, now() + final_log_grace);
        }
        if (view) {
            view->feed(line);
        }
    };

    // Keep the original output and parse complete lines even when writes span several reads.
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
        if (log_fd >= 0) {
            write_all(log_fd, buffer, static_cast<std::size_t>(count));
        }
        if (!view) {
            write_all(STDERR_FILENO, buffer, static_cast<std::size_t>(count));
        }
        std::string_view chunk(buffer, static_cast<std::size_t>(count));
        while (!chunk.empty()) {
            const auto newline = chunk.find('\n');
            pending.append(chunk.substr(0, std::min(newline, max_log_line - pending.size())));
            if (newline == chunk.npos) {
                break;
            }
            observe(pending);
            pending.clear();
            chunk.remove_prefix(newline + 1);
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
            if (log_fd >= 0) {
                close(log_fd);
            }
            if (!pending.empty()) {
                observe(pending);
            }
            // 124 matches timeout(1). 75 is EX_TEMPFAIL: retry after the limit resets.
            int result = WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
            std::string failure;
            if (timed_out && !log.failed()) {
                result = 124;
                failure = "lego did not finish within " + std::to_string(limit) + "s and was stopped with SIGKILL.";
            } else if (log.failed() || result != 0) {
                if (log.rate_limited()) {
                    result = 75;
                } else if (result == 0) {
                    result = 1;
                }
                failure = log.diagnostic(result);
                if (killed) {
                    failure += " (lego stopped)";
                }
            } else if (certificate && (!nonempty_file(std::string(certificate) + ".crt") ||
                                       !nonempty_file(std::string(certificate) + ".key"))) {
                result = 1;
                failure = "lego finished, but no certificate and key pair was saved.";
            }
            if (!failure.empty() && group) {
                failure.insert(0, std::string(group) + ": ");
            }
            if (view) {
                view->finish(failure, now());
            } else if (!failure.empty()) {
                const bool color = isatty(STDERR_FILENO);
                const std::string error = (color ? "\033[1;31m" : "") + std::string("❌ ") + failure +
                                          (color ? "\033[0m\n" : "\n");
                write_all(STDERR_FILENO, error.data(), error.size());
            }
            return result;
        }

        if (forwarded_signal != 0) {
            signal_lego(child, forwarded_signal);
            forwarded_signal = 0;
        }
        const double remaining = stop_at - now();
        if (!killed && remaining <= 0) {
            timed_out = !log.failed();
            signal_lego(child, SIGKILL);
            killed = true;
        }

        if (view) {
            view->tick(now());
        }
        const double step = view ? 0.1 : 1.0;
        double wait_for = killed ? 0.1 : (remaining < step ? remaining : step);
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
