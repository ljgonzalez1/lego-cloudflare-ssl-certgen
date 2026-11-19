#include "lego_log.hpp"

namespace certgen {
namespace {
constexpr std::string_view rate_limit = " :: urn:ietf:params:acme:error:rateLimited :: ";

bool contains(std::string_view text, std::string_view part) {
    return text.find(part) != std::string_view::npos;
}

bool has_timestamp(std::string_view line) {
    constexpr std::string_view format = "0000/00/00 00:00:00 ";
    if (line.size() < format.size()) {
        return false;
    }
    for (std::size_t index = 0; index < format.size(); ++index) {
        const auto character = line[index];
        const bool matches = format[index] == '0' ? character >= '0' && character <= '9'
                                                   : character == format[index];
        if (!matches) {
            return false;
        }
    }
    return true;
}
}

bool starts_with(std::string_view text, std::string_view prefix) {
    return text.substr(0, prefix.size()) == prefix;
}

// Keep a log detail to one printable line. The original is saved in lego.log.
std::string short_log_text(std::string_view text) {
    constexpr std::size_t limit = 220;
    std::string result;
    for (const auto character : text) {
        if (static_cast<unsigned char>(character) <= ' ' || character == '\x7f') {
            if (!result.empty() && result.back() != ' ') {
                result += ' ';
            }
        } else {
            result += character;
        }
        if (result.size() > limit) {
            auto end = limit;
            while (end > 0 && (static_cast<unsigned char>(result[end]) & 0xC0) == 0x80) {
                --end;
            }
            result.resize(end);
            return result + "...";
        }
    }
    if (!result.empty() && result.back() == ' ') {
        result.pop_back();
    }
    return result;
}

LegoLine::LegoLine(std::string_view text) : message(text) {
    if (has_timestamp(message)) {
        timestamp = message.substr(0, 19);
        message.remove_prefix(20);
    }
    const auto start = message.find_first_not_of(" \t\r");
    message = start == message.npos ? std::string_view{} : message.substr(start);
    if (starts_with(message, "[INFO] ")) {
        message.remove_prefix(7);
        level = Level::info;
    } else if (starts_with(message, "[WARN] ")) {
        message.remove_prefix(7);
        level = Level::warning;
    }
}

bool LegoLine::fatal_header() const {
    // lego v4 logs fatal CLI errors without an INFO or WARN prefix.
    constexpr std::string_view prefixes[] = {
        "Could not obtain certificates:", "Could not complete registration", "Could not create client:",
        "Could not load file for account ", "Could not parse file for account ", "Could not load account for ",
        "Could not save account for ", "Could not check/create", "Unable to save ",
        "cloudflare: some credentials information are missing",
        "Unable to marshal CertResource ", "Server requires External Account Binding.",
        "You did not accept the TOS.", "panic: ", "fatal error: "
    };
    if (level == Level::text) {
        for (const auto prefix : prefixes) {
            if (starts_with(message, prefix)) {
                return true;
            }
        }
    }
    return false;
}

bool LegoLine::domain_failure() const {
    if (level != Level::text) {
        return false;
    }
    auto detail = message;
    while (starts_with(detail, "[")) {
        const auto end = detail.find("] ");
        if (end == detail.npos) {
            return false;
        }
        detail.remove_prefix(end + 2);
    }
    return starts_with(detail, "acme: error presenting token:");
}

bool LegoLog::failed() const {
    return cause_ != Cause::none;
}

bool LegoLog::rate_limited() const {
    return cause_ == Cause::rate_limit;
}

void LegoLog::record_failure(std::string_view message) {
    // The first useful detail wins unless a more specific cause arrives in the same block.
    if (message.empty() || starts_with(message, "error: one or more domains had a problem:")) {
        return;
    }
    auto cause = Cause::fatal;
    if (const auto marker = message.find(rate_limit); marker != message.npos) {
        cause = Cause::rate_limit;
        message.remove_prefix(marker + rate_limit.size());
    } else if (contains(message, ":tooManyIdentifiers") || contains(message, "too many identifiers") ||
               contains(message, "too many domains")) {
        cause = Cause::name_limit;
    } else if (contains(message, "cloudflare: ")) {
        std::string error(message);
        for (auto& character : error) {
            if (character >= 'A' && character <= 'Z') {
                character += 'a' - 'A';
            }
        }
        if (contains(error, "invalid access token") || contains(error, "invalid api token") ||
            contains(error, "invalid format for authorization header") || contains(error, "9109:") ||
            contains(error, "6111:") || contains(error, "[status code 401]")) {
            cause = Cause::token;
        } else if (contains(error, "authentication error") || contains(error, "[status code 403]") ||
                   contains(error, "10000:")) {
            cause = Cause::access;
        } else if (contains(error, "failed to find zone ") || contains(error, "could not find zone for domain")) {
            cause = Cause::zone;
        }
    } else if (contains(message, "propagation: time limit exceeded")) {
        cause = Cause::propagation;
    } else if (contains(message, "invalid authorization") || contains(message, "invalid challenge") ||
               contains(message, ":unauthorized :: ") || contains(message, ":rejectedIdentifier :: ") ||
               contains(message, ":caa :: ")) {
        cause = Cause::rejected;
    }
    if (cause > cause_ || (cause == cause_ && detail_.empty())) {
        cause_ = cause;
        detail_ = short_log_text(message);
    }
}

void LegoLog::observe(const LegoLine& line) {
    const auto message = line.message;
    const bool fatal = line.fatal_header();
    // A typed ACME rate-limit response is definitive for this run even in a retry log.
    if (starts_with(message, "retry: acme: error: ") && contains(message, rate_limit)) {
        record_failure(message);
    } else if (fatal || line.domain_failure()) {
        if (fatal && header_.empty()) {
            header_ = short_log_text(message);
        }
        record_failure(message);
        if (detail_ == header_) {
            detail_.clear();
        }
    } else if (!header_.empty() && line.level == LegoLine::Level::text && line.timestamp.empty()) {
        record_failure(message);
    } else if (line.level == LegoLine::Level::text && (!line.timestamp.empty() || starts_with(message, "[ERROR] "))) {
        last_message_ = short_log_text(message);
    }
}

std::string LegoLog::diagnostic(int status) const {
    switch (cause_) {
    case Cause::rate_limit:
        return "Let's Encrypt rate limit reached." + (detail_.empty() ? " Wait before retrying."
                                                                      : " ACME response: " + detail_);
    case Cause::name_limit:
        return "Let's Encrypt rejected the number of DNS names. Use at most 100 per certificate.";
    case Cause::token:
        return "Cloudflare rejected the API token. Check that it is valid and active.";
    case Cause::access:
        return "Cloudflare denied API access. Check the token, Zone Read, DNS Edit and the zone scope.";
    case Cause::zone:
        return "Cloudflare zone lookup failed. Check DNS delegation and the token's Zone Read and zone scope.";
    case Cause::propagation:
        return "DNS propagation timed out. Check the challenge TXT records and DNS resolvers.";
    case Cause::rejected:
        return "Let's Encrypt rejected the order. " + detail_;
    case Cause::fatal:
        return header_.empty() ? detail_ : header_ + (detail_.empty() ? "" : " " + detail_);
    case Cause::none:
        const auto message = "lego exited with code " + std::to_string(status) + '.';
        return last_message_.empty() ? message : message + ' ' + last_message_;
    }
    return {};
}
}
