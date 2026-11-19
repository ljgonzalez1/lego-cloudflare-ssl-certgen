#include "core.hpp"

#include <poll.h>
#include <signal.h>
#include <sys/ioctl.h>
#include <termios.h>
#include <unistd.h>

#include <algorithm>
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
    const std::string_view terminal = term ? term : "";
    return !terminal.empty() && terminal != "dumb" && terminal != "unknown" && (!no_color || !*no_color);
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
            std::cerr << "\033[?1049h\033[?25l\033[?2004h";
        }
    }

    ~MenuTerminal() {
        while (tcsetattr(STDIN_FILENO, TCSAFLUSH, &original_) != 0 && errno == EINTR) {
        }
        if (ansi) {
            std::cerr << "\033[?2004l\033[0m\033[?25h\033[?1049l";
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
        if (count < 0 && (errno == EINTR || errno == EAGAIN)) {
            continue;
        }
        if (count < 0 && (input.revents & POLLHUP) == 0) {
            terminal_error("could not read terminal input");
        }
        if (count == 1 && key == 3) {  // Ctrl+C
            interrupted = SIGINT;
            return cancel;
        }
        return count == 1 ? key : cancel;
    }
    return cancel;
}

enum class Key { previous, next, enter, back, cancel, none };

Key discard_paste() {
    constexpr std::string_view end = "\033[201~";
    std::size_t matched = 0;
    for (;;) {
        const auto byte = read_byte();
        if (byte == cancel) {
            return Key::cancel;
        }
        matched = byte == end[matched] ? matched + 1 : byte == end.front() ? 1 : 0;
        if (matched == end.size()) {
            return Key::none;
        }
    }
}

Key read_key() {
    const auto key = read_byte();
    switch (key) {
    case cancel:
    case 4:  // Ctrl+D
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
        if (prefix == cancel) {
            return Key::cancel;
        }
        if (prefix != '[' && prefix != 'O') {
            return Key::none;
        }
        std::string parameters;
        bool too_long = false;
        auto code = read_byte(120);
        while (code >= 32 && code < 64) {
            if (parameters.size() < 16) {
                parameters += static_cast<char>(code);
            } else {
                too_long = true;
            }
            code = read_byte(120);
        }
        if (code == cancel) {
            return Key::cancel;
        }
        if (too_long) {
            return Key::none;
        }
        if (prefix == '[' && parameters == "200" && code == '~') {
            return discard_paste();
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
          const std::vector<std::string_view>& options, std::size_t selected, bool alert) {
    winsize size{};
    const auto columns = ioctl(STDERR_FILENO, TIOCGWINSZ, &size) == 0 && size.ws_col ? size.ws_col : 80;
    // Leave one column free to avoid auto-wrapping at the right edge.
    const bool boxed = columns >= 22;
    const auto width = static_cast<std::size_t>(std::max(1, std::min(columns - 1, 76) - (boxed ? 4 : 0)));
    const std::string_view accent = ansi ? (alert ? "\033[33m" : "\033[36m") : "";
    const std::string_view reset = ansi ? "\033[0m" : "";
    std::string page = ansi ? "\033[H\033[2J" : "\n";
    if (ansi && columns >= 30) {
        page += alert ? "⚠️  Let's Encrypt production\n\n" : "🔐 Certificate issuance\n\n";
    }
    const auto rule = [&] {
        if (boxed) {
            page += std::string(accent) + '+' + std::string(width + 2, '-') + '+';
            page += reset;
            page += '\n';
        }
    };
    const auto row = [&](std::string_view content, std::size_t length) {
        if (boxed) {
            page += std::string(accent) + "| " + std::string(reset);
        }
        page += content;
        if (boxed) {
            page.append(width - length, ' ');
            page += std::string(accent) + " |" + std::string(reset);
        }
        page += '\n';
    };
    const auto wrap = [&](std::string_view line, bool bold = false) {
        do {
            auto count = std::min(width, line.size());
            const auto space = line.rfind(' ', count);
            if (count < line.size() && space != line.npos && space > 0) {
                count = space;
            }
            auto chunk = std::string(line.substr(0, count));
            if (ansi && bold) {
                chunk = "\033[1m" + chunk + "\033[0m";
            }
            row(chunk, count);
            line.remove_prefix(count);
            if (!line.empty() && line.front() == ' ') {
                line.remove_prefix(1);
            }
        } while (!line.empty());
    };

    rule();
    wrap(title, true);
    rule();
    for (const auto line : details) {
        wrap(line);
    }
    row("", 0);
    std::string buttons;
    std::size_t button_width = 0;
    for (std::size_t index = 0; index < options.size(); ++index) {
        const auto text = choice_text(options[index], index == selected);
        if (!buttons.empty() && button_width + text.size() > width) {
            row(buttons, button_width);
            buttons.clear();
            button_width = 0;
        }
        if (ansi && index == selected) {
            buttons += "\033[1;7m";
        }
        buttons += text;
        if (ansi && index == selected) {
            buttons += reset;
        }
        button_width += text.size();
    }
    row(buttons, button_width);
    rule();
    wrap("Arrows or Tab: move | Enter: select | Esc: back | Ctrl+C: exit");
    std::cerr << page;
}

int choose(bool ansi, std::string_view title, const std::vector<std::string_view>& details,
           const std::vector<std::string_view>& options, std::size_t selected, bool alert = false) {
    // Each screen needs fresh input. Discard keys already queued for the previous question.
    if (tcflush(STDIN_FILENO, TCIFLUSH) != 0 && !interrupted) {
        terminal_error("could not reset terminal input");
    }
    for (;;) {
        draw(ansi, title, details, options, selected, alert);
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
                      {"Yes", "No"}, 1, true);
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
        std::cerr << "The issuance menu needs a terminal. Use docker run -it, --test for offline checks, "
                     "or an explicit mode for unattended issuance.\n";
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
