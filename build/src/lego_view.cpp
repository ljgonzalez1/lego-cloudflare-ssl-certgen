#include "lego_view.hpp"

#include <sys/ioctl.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstdlib>

namespace certgen {
namespace {
constexpr const char* reset = "\033[0m";
constexpr const char* dim = "\033[2m";
constexpr const char* red = "\033[31m";
constexpr const char* green = "\033[32m";
constexpr const char* yellow = "\033[33m";
constexpr const char* blue = "\033[34m";
constexpr const char* cyan = "\033[36m";
constexpr const char* bold = "\033[1m";
constexpr const char* clear_line = "\r\033[2K";
constexpr std::string_view spinner_frames[] = {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"};

std::size_t environment_number(const char* name) {
    if (const auto* value = std::getenv(name)) {
        char* end = nullptr;
        const auto number = std::strtoul(value, &end, 10);
        if (end != value && *end == '\0') {
            return number;
        }
    }
    return 0;
}

// COLUMNS and LINES win, as in the summary table, so tests and wrappers can fix the size.
void terminal_size(int fd, std::size_t& rows, std::size_t& columns) {
    winsize size{};
    const bool known = ioctl(fd, TIOCGWINSZ, &size) == 0;
    columns = environment_number("COLUMNS");
    rows = environment_number("LINES");
    if (columns == 0) {
        columns = known && size.ws_col ? size.ws_col : 80;
    }
    if (rows == 0 && known) {
        rows = size.ws_row;
    }
}

// Terminal columns used by one code point. The markers used here are wide emoji.
std::size_t code_point_width(char32_t code) {
    if (code == 0xFE0F || code == 0x200D) {
        return 0;
    }
    if (code >= 0x1F000 || (code >= 0x2300 && code <= 0x23FF) || (code >= 0x2600 && code <= 0x27BF)) {
        return 2;
    }
    return 1;
}

// Cut text with ANSI colors to a display width, so no line ever wraps.
std::string fit(std::string_view text, std::size_t width) {
    std::string result;
    std::size_t used = 0;
    for (std::size_t index = 0; index < text.size();) {
        const auto byte = static_cast<unsigned char>(text[index]);
        if (byte == 0x1B) {
            const auto end = text.find('m', index);
            const auto stop = end == std::string_view::npos ? text.size() : end + 1;
            result.append(text.substr(index, stop - index));
            index = stop;
            continue;
        }
        std::size_t length = byte < 0x80 ? 1 : byte >= 0xF0 ? 4 : byte >= 0xE0 ? 3 : byte >= 0xC0 ? 2 : 1;
        length = std::min(length, text.size() - index);
        char32_t code = length == 1 ? byte : byte & (0xFF >> (length + 1));
        for (std::size_t next = 1; next < length; ++next) {
            code = (code << 6) | (static_cast<unsigned char>(text[index + next]) & 0x3F);
        }
        const auto columns = code_point_width(code);
        if (used + columns > width) {
            if (width > 0 && !result.empty()) {
                result += "…";
            }
            break;
        }
        result.append(text.substr(index, length));
        used += columns;
        index += length;
    }
    return result + reset;
}

std::string clock(double seconds) {
    const auto total = static_cast<unsigned long>(seconds < 0 ? 0 : seconds);
    char text[32];
    if (total >= 3600) {
        std::snprintf(text, sizeof(text), "%lu:%02lu:%02lu", total / 3600, total / 60 % 60, total % 60);
    } else {
        std::snprintf(text, sizeof(text), "%lu:%02lu", total / 60, total % 60);
    }
    return text;
}

std::string duration(double seconds) {
    const auto total = static_cast<unsigned long>(seconds < 0 ? 0 : seconds + 0.5);
    return total < 60 ? std::to_string(total) + "s"
                      : std::to_string(total / 60) + "m" + std::to_string(total % 60) + "s";
}

bool starts_with(std::string_view text, std::string_view prefix) {
    return text.substr(0, prefix.size()) == prefix;
}

bool timestamped(std::string_view line) {
    if (line.size() < 20) {
        return false;
    }
    for (const auto position : {4, 7}) {
        if (line[position] != '/') {
            return false;
        }
    }
    return line[10] == ' ' && line[13] == ':' && line[16] == ':' && line[19] == ' ';
}

void add_once(std::vector<std::string>& list, std::string text) {
    if (std::find(list.begin(), list.end(), text) == list.end()) {
        list.push_back(std::move(text));
    }
}
}

LegoView::LegoView(const std::string& names, std::uint64_t limit_seconds, int fd)
    : limit_(limit_seconds), fd_(fd) {
    std::size_t start = 0;
    while (start <= names.size()) {
        const auto comma = names.find(',', start);
        const auto value = names.substr(start, comma == std::string::npos ? std::string::npos : comma - start);
        if (!value.empty() && !index_.count(value)) {
            index_.emplace(value, names_.size());
            names_.push_back(Name{value, State::waiting, {}, {}, {}, 0});
            name_width_ = std::max(name_width_, value.size());
        }
        if (comma == std::string::npos) {
            break;
        }
        start = comma + 1;
    }
}

void LegoView::emit(const std::string& text) const {
    const char* data = text.data();
    auto size = text.size();
    while (size > 0) {
        const auto written = write(fd_, data, size);
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

void LegoView::start(double now) {
    started_ = now;
    std::size_t rows = 0;
    std::size_t columns = 0;
    terminal_size(fd_, rows, columns);
    // Keep the whole list on screen; otherwise lines that scrolled away could
    // not be updated, so finished names are printed below instead.
    list_ = rows >= names_.size() + 3;
    activity_ = "starting lego";
    if (list_) {
        std::string text;
        for (const auto& name : names_) {
            text += render(name, columns) + '\n';
        }
        emit(text);
    }
    draw_footer(now);
}

std::size_t LegoView::find(std::string_view name) const {
    const auto found = index_.find(name);
    return found == index_.end() ? npos : found->second;
}

std::size_t LegoView::verified() const {
    return static_cast<std::size_t>(std::count_if(names_.begin(), names_.end(), [](const Name& name) {
        return name.state == State::verified;
    }));
}

std::string LegoView::render(const Name& name, std::size_t columns) const {
    const char* color = blue;
    const char* marker = "🔹";
    switch (name.state) {
    case State::waiting:
        break;
    case State::active:
        color = yellow;
        marker = "⏳";
        break;
    case State::verified:
        color = green;
        marker = "✅";
        break;
    case State::failed:
        color = red;
        marker = "❌";
        break;
    }
    const auto width = std::min(name_width_, std::max<std::size_t>(columns / 2, 16));
    std::string text = std::string(color) + marker + ' ' + name.value;
    if (!name.label.empty()) {
        if (name.value.size() < width) {
            text.append(width - name.value.size(), ' ');
        }
        text += std::string(reset) + "  " + (name.state == State::failed ? red : dim) + name.label;
    }
    return fit(text, columns - 1);
}

std::string LegoView::footer(double now, std::size_t columns) const {
    std::string text = std::string(yellow) + std::string(spinner_frames[spinner_ % 10]) + ' ' + cyan +
                       clock(now - started_) + " / " + clock(static_cast<double>(limit_)) + " · " +
                       std::to_string(verified()) + '/' + std::to_string(names_.size()) + " verified";
    if (!activity_.empty()) {
        text += std::string(reset) + dim + " · " + activity_;
    }
    return fit(text, columns - 1);
}

void LegoView::draw_name(std::size_t index, std::size_t columns) {
    if (!list_) {
        return;
    }
    const auto up = names_.size() - index;
    emit("\033[" + std::to_string(up) + "A" + clear_line + render(names_[index], columns) + "\033[" +
         std::to_string(up) + "B\r");
}

void LegoView::draw_footer(double now) {
    std::size_t rows = 0;
    std::size_t columns = 0;
    terminal_size(fd_, rows, columns);
    emit(clear_line + footer(now, columns));
}

void LegoView::tick(double now) {
    ++spinner_;
    draw_footer(now);
}

void LegoView::set(std::size_t index, State state, std::string label, double now) {
    auto& name = names_[index];
    if (name.state == State::verified || (name.state == State::failed && state != State::failed)) {
        return;
    }
    if (state == State::active && name.state != State::active) {
        name.started = now;
    }
    const bool changed = name.state != state || name.label != label;
    name.state = state;
    name.label = std::move(label);
    if (state == State::active) {
        activity_ = name.value + ": " + name.label;
    }
    if (!changed) {
        return;
    }
    std::size_t rows = 0;
    std::size_t columns = 0;
    terminal_size(fd_, rows, columns);
    if (list_) {
        draw_name(index, columns);
    } else if (state == State::verified) {
        emit(clear_line + render(name, columns) + '\n');
    }
}

void LegoView::feed(std::string_view text, double now) {
    pending_.append(text);
    std::size_t newline;
    while ((newline = pending_.find('\n')) != std::string::npos) {
        const auto current = pending_.substr(0, newline);
        pending_.erase(0, newline + 1);
        line(current, now);
    }
    draw_footer(now);
}

void LegoView::line(std::string_view text, double now) {
    if (!text.empty() && text.back() == '\r') {
        text.remove_suffix(1);
    }
    if (!timestamped(text)) {
        // Continuation lines of a fatal error. Anything else without a
        // timestamp is lego's advice text on stdout, kept in the log file only.
        if (in_error_) {
            error_line(text);
        }
        return;
    }
    // Durations come from lego's timestamps, which stay exact when output is delayed.
    const auto digits = [&](std::size_t at) { return (text[at] - '0') * 10 + (text[at + 1] - '0'); };
    const double raw = digits(11) * 3600.0 + digits(14) * 60.0 + digits(17);
    // Only a jump back of more than 12 hours is midnight; small steps back are clock noise.
    if (last_raw_stamp_ >= 0 && raw + 43200 < last_raw_stamp_) {
        day_offset_ += 86400;
    }
    last_raw_stamp_ = raw;
    stamp_ = raw + day_offset_;
    if (first_stamp_ < 0) {
        first_stamp_ = stamp_;
    }
    now = stamp_;

    auto message = text.substr(20);
    bool warning = false;
    if (starts_with(message, "[INFO] ")) {
        message.remove_prefix(7);
    } else if (starts_with(message, "[WARN] ")) {
        message.remove_prefix(7);
        warning = true;
    }

    if (starts_with(message, "Could not ")) {
        in_error_ = true;
        const auto colon = message.find(':');
        if (colon != std::string_view::npos && colon + 1 < message.size()) {
            add_once(errors_, std::string(message));
        }
        return;
    }
    if (starts_with(message, "retry: ")) {
        add_once(errors_, std::string(message.substr(7)));
        activity_ = message.find("rateLimited") != std::string_view::npos ? "Let's Encrypt rate limit reached"
                                                                            : "retrying a request";
        return;
    }
    // Messages about the whole order. lego names every domain in brackets.
    if (message.find("acme: Obtaining ") != std::string_view::npos) {
        activity_ = "creating the order";
        return;
    }
    if (message.find("acme: Validations succeeded") != std::string_view::npos) {
        activity_ = "requesting the certificate";
        return;
    }
    if (message.find("Server responded with a certificate") != std::string_view::npos) {
        issued_ = true;
        activity_ = "certificate received";
        return;
    }
    if (starts_with(message, "[")) {
        const auto close = message.find("] ");
        if (close != std::string_view::npos) {
            const auto index = find(message.substr(1, close - 1));
            if (index != npos) {
                domain_event(index, message.substr(close + 2), now);
                return;
            }
        }
    }
    if (starts_with(message, "No key found for account")) {
        activity_ = "creating the ACME account key";
    } else if (starts_with(message, "acme: Registering account")) {
        activity_ = "registering the ACME account";
    } else if (starts_with(message, "cloudflare: new record for")) {
        if (current_ != npos && names_[current_].state != State::verified) {
            set(current_, State::waiting, "TXT record ready", now);
            activity_ = names_[current_].value + ": TXT record ready";
        }
    } else if (starts_with(message, "Wait for propagation [timeout: ")) {
        if (current_ != npos) {
            const auto end = message.find(',');
            const auto limit = message.substr(31, end == std::string_view::npos ? 0 : end - 31);
            set(current_, State::active, "waiting for DNS propagation (up to " + std::string(limit) + ")", now);
        }
    } else if (starts_with(message, "Deactivating auth: ")) {
        const auto found = authorizations_.find(std::string(message.substr(19)));
        if (found != authorizations_.end() && names_[found->second].state != State::verified) {
            set(found->second, State::failed, "not verified", now);
        }
    } else if (starts_with(message, "sequence: wait for")) {
        activity_ = "waiting before the next challenge";
    } else if (warning || starts_with(message, "cloudflare: failed")) {
        add_once(warnings_, std::string(message));
    }
}

void LegoView::domain_event(std::size_t index, std::string_view message, double now) {
    if (starts_with(message, "AuthURL: ")) {
        authorizations_[std::string(message.substr(9))] = index;
        activity_ = "loading authorizations";
    } else if (starts_with(message, "acme: authorization already valid")) {
        set(index, State::verified, "already verified", now);
    } else if (starts_with(message, "acme: Preparing to solve DNS-01")) {
        current_ = index;
        set(index, State::active, "creating TXT record", now);
    } else if (starts_with(message, "acme: Trying to solve DNS-01")) {
        current_ = index;
        names_[index].started = now;
        set(index, State::active, "checking DNS", now);
    } else if (starts_with(message, "acme: Checking DNS record propagation")) {
        set(index, State::active, "checking DNS propagation", now);
    } else if (starts_with(message, "acme: Waiting for DNS record propagation")) {
        set(index, State::active, "waiting for DNS propagation", now);
    } else if (starts_with(message, "The server validated our request")) {
        set(index, State::verified, "verified in " + duration(now - names_[index].started), now);
    } else if (starts_with(message, "acme: Cleaning DNS-01 challenge")) {
        activity_ = "removing TXT records";
    } else if (starts_with(message, "acme: cleaning up failed: ")) {
        names_[index].cleanup = std::string(message.substr(26));
        while (!names_[index].cleanup.empty() && names_[index].cleanup.back() == ' ') {
            names_[index].cleanup.pop_back();
        }
    }
}

void LegoView::error_line(std::string_view text) {
    while (!text.empty() && (text.front() == '\t' || text.front() == ' ')) {
        text.remove_prefix(1);
    }
    if (text.empty() || starts_with(text, "error: one or more domains had a problem")) {
        return;
    }
    std::size_t index = npos;
    // "[name] [name] reason" from the challenge resolver.
    while (starts_with(text, "[")) {
        const auto close = text.find("] ");
        if (close == std::string_view::npos) {
            break;
        }
        const auto found = find(text.substr(1, close - 1));
        if (found == npos) {
            break;
        }
        index = found;
        text.remove_prefix(close + 2);
    }
    // "name: reason" from certificate errors.
    if (index == npos) {
        const auto colon = text.find(": ");
        if (colon != std::string_view::npos) {
            index = find(text.substr(0, colon));
            if (index != npos) {
                text.remove_prefix(colon + 2);
            }
        }
    }
    if (index == npos) {
        add_once(errors_, std::string(text));
        return;
    }
    names_[index].reason = std::string(text);
    if (starts_with(text, "acme: ")) {
        text.remove_prefix(6);
    }
    set(index, State::failed, std::string(text), stamp_);
}

void LegoView::finish(int status, int reason, double now) {
    if (!pending_.empty()) {
        const auto rest = pending_;
        pending_.clear();
        line(rest, now);
    }
    const bool success = status == 0 && reason == 0;
    std::size_t rows = 0;
    std::size_t columns = 0;
    terminal_size(fd_, rows, columns);

    // No certificate covers a name that did not finish, so it counts as failed.
    if (!success) {
        const std::string label = reason == 124  ? "stopped by the time limit"
                                  : reason == 75 ? "stopped by the rate limit"
                                                 : "not verified";
        for (std::size_t index = 0; index < names_.size(); ++index) {
            const auto state = names_[index].state;
            if (state == State::waiting || state == State::active) {
                set(index, State::failed, label, now);
            }
        }
    }
    std::vector<std::size_t> failed;
    for (std::size_t index = 0; index < names_.size(); ++index) {
        if (names_[index].state == State::failed) {
            failed.push_back(index);
        }
    }
    if (!list_) {
        // Compact view: failed names appear once, here. Long runs of names
        // stopped for the same reason collapse into one line.
        std::string text;
        std::size_t collapsed = 0;
        std::string collapsed_label;
        for (const auto index : failed) {
            if (failed.size() > 10 && names_[index].reason.empty()) {
                ++collapsed;
                collapsed_label = names_[index].label;
            } else {
                text += clear_line + render(names_[index], columns) + '\n';
            }
        }
        if (collapsed > 0) {
            text += clear_line + fit(std::string(red) + "❌ " + std::to_string(collapsed) + " names " +
                                         collapsed_label, columns - 1) + '\n';
        }
        emit(text);
    }

    const auto total = std::to_string(names_.size());
    const auto ok = std::to_string(verified());
    // lego's own timestamps cover the run exactly, even when its output was buffered.
    const double lego_span = first_stamp_ < 0 ? 0 : stamp_ - first_stamp_;
    const auto elapsed = duration(std::max(now - started_, lego_span));
    const std::string result =
        success ? std::string(bold) + green + "✅ Certificate received · " + ok + '/' + total +
                      " names verified · " + elapsed
                : std::string(bold) + red + "❌ No certificate · " + ok + '/' + total + " names verified · " +
                      std::to_string(failed.size()) + " failed · " + elapsed;
    std::string text = clear_line + fit(result, columns - 1) + '\n';

    // Details stay below the live view as normal lines, so they may wrap.
    for (const auto& name : names_) {
        if (!name.reason.empty()) {
            text += std::string(red) + "   " + name.value + ": " + name.reason + reset + '\n';
        }
    }
    for (const auto& error : errors_) {
        text += std::string(red) + "   " + error + reset + '\n';
    }
    // A failed cleanup matters only for verified names: their TXT record stays in Cloudflare.
    for (const auto& name : names_) {
        if (!name.cleanup.empty() && name.state == State::verified) {
            add_once(warnings_, name.value + ": TXT record not removed: " + name.cleanup);
        }
    }
    for (std::size_t index = 0; index < warnings_.size() && index < 5; ++index) {
        text += std::string(yellow) + "   ⚠️  " + warnings_[index] + reset + '\n';
    }
    emit(text);
}
}
