#include "core.hpp"

#include <poll.h>
#include <signal.h>
#include <termios.h>
#include <unistd.h>

#include <cerrno>
#include <cstdlib>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <system_error>
#include <vector>

namespace certgen {
namespace {
volatile sig_atomic_t interrupted = 0;
constexpr int signals[] = {SIGINT, SIGTERM, SIGHUP, SIGQUIT};
constexpr int back = -1;
constexpr int cancel = -2;
constexpr int no_byte = -3;

void record_signal(int number) {
    interrupted = number;
}

[[noreturn]] void terminal_error(const char* message) {
    throw std::system_error(errno, std::generic_category(), message);
}

class MenuSignals {
public:
    MenuSignals() {
        interrupted = 0;
        struct sigaction action {};
        action.sa_handler = record_signal;
        sigemptyset(&action.sa_mask);
        for (const auto number : signals) {
            if (sigaction(number, &action, &previous_[installed_]) != 0) {
                restore();
                terminal_error("could not install terminal signal handler");
            }
            ++installed_;
        }
    }

    ~MenuSignals() { restore(); }
    MenuSignals(const MenuSignals&) = delete;
    MenuSignals& operator=(const MenuSignals&) = delete;

private:
    struct sigaction previous_[sizeof(signals) / sizeof(signals[0])]{};
    std::size_t installed_ = 0;

    void restore() {
        while (installed_ > 0) {
            --installed_;
            sigaction(signals[installed_], &previous_[installed_], nullptr);
        }
    }
};

bool supports_ansi() {
    const auto* term = std::getenv("TERM");
    const auto* no_color = std::getenv("NO_COLOR");
    return term && *term && std::string_view(term) != "dumb" && (!no_color || !*no_color);
}

class MenuTerminal {
public:
    MenuTerminal() : ansi(supports_ansi()) {
        if (tcgetattr(STDIN_FILENO, &original_) != 0) {
            terminal_error("could not read terminal settings");
        }
        auto input = original_;
        input.c_lflag &= ~(ICANON | ECHO | ISIG | IEXTEN);
        input.c_iflag &= ~(IXON | IXOFF);
        input.c_cc[VMIN] = 1;
        input.c_cc[VTIME] = 0;
        if (tcsetattr(STDIN_FILENO, TCSAFLUSH, &input) != 0) {
            terminal_error("could not enable interactive input");
        }
        if (ansi) {
            std::cerr << "\033[?1049h\033[?25l";
        }
    }

    ~MenuTerminal() {
        while (tcsetattr(STDIN_FILENO, TCSAFLUSH, &original_) != 0 && errno == EINTR) {
        }
        if (ansi) {
            std::cerr << "\033[0m\033[?25h\033[?1049l";
        }
    }

    MenuTerminal(const MenuTerminal&) = delete;
    MenuTerminal& operator=(const MenuTerminal&) = delete;
    const bool ansi;

private:
    termios original_{};
};

int read_byte(int timeout = -1) {
    while (!interrupted) {
        pollfd input{STDIN_FILENO, POLLIN, 0};
        // Check for a signal even if it arrived just before poll().
        const auto ready = poll(&input, 1, timeout < 0 ? 200 : timeout);
        if (ready < 0 && errno == EINTR) {
            continue;
        }
        if (ready < 0) {
            terminal_error("could not wait for terminal input");
        }
        if (ready == 0) {
            if (timeout >= 0) {
                return no_byte;
            }
            continue;
        }
        unsigned char key = 0;
        const auto count = read(STDIN_FILENO, &key, 1);
        if (count < 0 && errno == EINTR) {
            continue;
        }
        if (count < 0 && (input.revents & POLLHUP) == 0) {
            terminal_error("could not read terminal input");
        }
        return count == 1 ? key : cancel;
    }
    return cancel;
}

enum class Key { previous, next, enter, back, cancel, none };

Key read_key() {
    const auto key = read_byte();
    switch (key) {
    case cancel:
    case 4:  // Ctrl+D
        return Key::cancel;
    case 3:  // Ctrl+C
        interrupted = SIGINT;
        return Key::cancel;
    case '\r':
    case '\n':
        return Key::enter;
    case 'h':
    case 'k':
        return Key::previous;
    case '\t':
    case 'j':
    case 'l':
        return Key::next;
    case 27: {
        const auto prefix = read_byte(120);
        if (prefix == no_byte || prefix == 27) {
            return Key::back;
        }
        if (prefix != '[' && prefix != 'O') {
            return Key::none;
        }
        auto code = read_byte(120);
        for (int remaining = 12; remaining > 0 && code >= 32 && code < 64; --remaining) {
            code = read_byte(120);
        }
        switch (code) {
        case 'A':
        case 'D':
        case 'Z':
            return Key::previous;
        case 'B':
        case 'C':
            return Key::next;
        default:
            return Key::none;
        }
    }
    default:
        return Key::none;
    }
}

std::string choice_text(std::string_view label, bool selected) {
    return selected ? " > " + std::string(label) + " < " : "   " + std::string(label) + "   ";
}

void draw(bool ansi, std::string_view title, const std::vector<std::string_view>& details,
          const std::vector<std::string_view>& options, std::size_t selected) {
    std::string page = ansi ? "\033[H\033[2J\033[1;36m" : "\n";
    page += title;
    page += ansi ? "\033[0m\n\n" : "\n\n";
    for (const auto line : details) {
        page += line;
        page += '\n';
    }
    page += '\n';
    for (std::size_t index = 0; index < options.size(); ++index) {
        if (ansi && index == selected) {
            page += "\033[1;7m";
        }
        page += choice_text(options[index], index == selected);
        if (ansi && index == selected) {
            page += "\033[0m";
        }
    }
    page += "\n\nArrows or Tab: move | Enter: select | Esc: back | Ctrl+C: exit\n";
    std::cerr << page;
}

int choose(bool ansi, std::string_view title, const std::vector<std::string_view>& details,
           const std::vector<std::string_view>& options, std::size_t selected) {
    // Each screen needs fresh input. Discard keys already queued for the previous question.
    if (tcflush(STDIN_FILENO, TCIFLUSH) != 0 && !interrupted) {
        terminal_error("could not reset terminal input");
    }
    for (;;) {
        draw(ansi, title, details, options, selected);
        for (;;) {
            switch (read_key()) {
            case Key::previous: selected = (selected + options.size() - 1) % options.size(); break;
            case Key::next: selected = (selected + 1) % options.size(); break;
            case Key::enter: return static_cast<int>(selected);
            case Key::back: return back;
            case Key::cancel: return cancel;
            case Key::none: continue;
            }
            break;
        }
    }
}

int confirm(bool ansi, bool production) {
    if (production) {
        return choose(ansi, "Confirm production issuance",
                      {"This submits live orders for browser-trusted certificates.",
                       "Default cap: 50 new certificates per registered domain per 7 days.",
                       "An identical set of names is also capped at 5 per 7 days.",
                       "https://letsencrypt.org/docs/rate-limits/"},
                      {"Yes", "No"}, 1);
    }
    return choose(ansi, "Confirm staging issuance",
                  {"This contacts Let's Encrypt staging and updates DNS challenge records.",
                   "Browsers do not trust the resulting certificates."},
                  {"Yes", "No"}, 1);
}

std::string_view select_mode(bool ansi) {
    for (;;) {
        const auto selected = choose(ansi, "Choose an issuance environment",
                                     {"Production issues browser-trusted certificates and has rate limits.",
                                      "Staging tests DNS and issuance with untrusted certificates."},
                                     {"Staging", "Production", "Cancel"}, 0);
        if (selected < 0 || selected == 2) {
            return "cancel";
        }
        const bool production = selected == 1;
        const auto accepted = confirm(ansi, production);
        if (accepted == cancel) {
            return "cancel";
        }
        if (accepted == 0) {
            return production ? "production" : "staging";
        }
    }
}
}

int issuance_menu() {
    if (!isatty(STDIN_FILENO) || !isatty(STDERR_FILENO)) {
        std::cerr << "The issuance menu needs a terminal. Use docker run -it, or --test for offline checks.\n";
        return 2;
    }
    MenuSignals signals_guard;
    std::string_view result;
    {
        MenuTerminal terminal;
        result = select_mode(terminal.ansi);
    }
    if (interrupted) {
        std::cerr << "Certificate issuance cancelled.\n";
        return 128 + interrupted;
    }
    std::cout << result << '\n';
    return std::cout ? 0 : 2;
}
}
