#include <sys/wait.h>
#include <fcntl.h>
#include <signal.h>
#include <unistd.h>

#include "core.hpp"

#include <algorithm>
#include <cerrno>
#include <iostream>
#include <system_error>

namespace certgen {
namespace {
volatile sig_atomic_t interrupted_by = 0;
volatile sig_atomic_t active_worker = 0;
constexpr int termination_signals[] = {SIGHUP, SIGINT, SIGTERM};

void forward_termination(int signal_number) {
    const int saved_errno = errno;
    if (interrupted_by == 0) {
        interrupted_by = signal_number;
    }
    if (active_worker > 0) {
        // The supervisor owns lego's process group and handles its cleanup.
        kill(active_worker, SIGTERM);
    }
    errno = saved_errno;
}

[[noreturn]] void process_error(const char* message) {
    throw std::system_error(errno, std::generic_category(), message);
}

void fill_termination_mask(sigset_t& mask) {
    sigemptyset(&mask);
    for (const auto signal_number : termination_signals) {
        sigaddset(&mask, signal_number);
    }
}

struct Pipe {
    int read_end = -1;
    int write_end = -1;

    explicit Pipe(bool open_pipe) {
        if (open_pipe) {
            int ends[2];
            if (pipe2(ends, O_CLOEXEC) != 0) {
                process_error("Cannot capture lego output");
            }
            read_end = ends[0];
            write_end = ends[1];
        }
    }
    ~Pipe() {
        if (read_end >= 0) {
            close(read_end);
        }
        if (write_end >= 0) {
            close(write_end);
        }
    }
};

int supervise_child(std::vector<char*>& arguments, const Pipe& pipe) {
    if (pipe.write_end >= 0) {
        const int input = open("/dev/null", O_RDONLY | O_CLOEXEC);
        if (input < 0 || dup2(input, STDIN_FILENO) < 0 || dup2(pipe.write_end, STDERR_FILENO) < 0) {
            process_error("Cannot redirect the lego version check");
        }
        close(input);
        close(pipe.read_end);
        close(pipe.write_end);
    }
    return supervise(static_cast<int>(arguments.size() - 1), arguments.data());
}
}

int cancellation_status() {
    return interrupted_by ? 128 + interrupted_by : 0;
}

void watch_termination() {
    struct sigaction action {};
    action.sa_handler = forward_termination;
    fill_termination_mask(action.sa_mask);
    for (const auto signal_number : termination_signals) {
        if (sigaction(signal_number, &action, nullptr) != 0) {
            process_error("Cannot watch for interruption");
        }
    }
}

int run_lego(std::vector<std::string> arguments, std::string* output) {
    std::vector<char*> argv;
    argv.reserve(arguments.size() + 1);
    for (auto& argument : arguments) {
        argv.push_back(argument.data());
    }
    argv.push_back(nullptr);
    Pipe pipe(output != nullptr);
    std::cout.flush();
    std::cerr.flush();
    sigset_t blocked;
    sigset_t previous;
    fill_termination_mask(blocked);
    if (sigprocmask(SIG_BLOCK, &blocked, &previous) != 0) {
        process_error("Cannot block termination signals");
    }
    if (cancellation_status() != 0) {
        sigprocmask(SIG_SETMASK, &previous, nullptr);
        return cancellation_status();
    }
    const auto worker = fork();
    if (worker == 0) {
        // Keep termination signals blocked until the supervisor can handle them.
        try {
            _exit(supervise_child(argv, pipe));
        } catch (const std::exception& error) {
            std::cerr << "Cannot supervise lego: " << error.what() << '\n';
            _exit(1);
        }
    }
    const int fork_error = errno;
    active_worker = worker > 0 ? worker : 0;
    sigprocmask(SIG_SETMASK, &previous, nullptr);
    if (worker < 0) {
        errno = fork_error;
        process_error("Cannot start the lego supervisor");
    }
    if (output) {
        close(pipe.write_end);
        pipe.write_end = -1;
        char bytes[1024];
        for (;;) {
            const auto count = read(pipe.read_end, bytes, sizeof(bytes));
            if (count == 0 || (count < 0 && errno != EINTR)) {
                break;
            }
            if (count > 0 && output->size() < sizeof(bytes)) {
                output->append(bytes, std::min(static_cast<std::size_t>(count), sizeof(bytes) - output->size()));
            }
        }
    }
    int status = 0;
    pid_t waited;
    do {
        waited = waitpid(worker, &status, 0);
    } while (waited < 0 && errno == EINTR);
    active_worker = 0;
    if (waited < 0) {
        process_error("Cannot wait for the lego supervisor");
    }
    if (const auto cancelled = cancellation_status(); cancelled != 0) {
        return cancelled;
    }
    const int result = WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
    if (!output && (result == 128 + SIGHUP || result == 128 + SIGINT || result == 128 + SIGTERM)) {
        interrupted_by = result - 128;
    }
    return result;
}
}
