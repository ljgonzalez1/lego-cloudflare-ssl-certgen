#include "app.hpp"

#include <arpa/inet.h>
#include <grp.h>
#include <unistd.h>

#include <algorithm>
#include <cctype>
#include <charconv>
#include <cerrno>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <iterator>
#include <sstream>
#include <system_error>

namespace certgen {
namespace {
bool parse_unsigned(std::string_view text, unsigned limit, int digits, unsigned& value) {
    if (text.empty() || text.size() > static_cast<std::size_t>(digits)) {
        return false;
    }
    const auto result = std::from_chars(text.data(), text.data() + text.size(), value);
    return result.ec == std::errc{} && result.ptr == text.data() + text.size() && value <= limit;
}

unsigned read_id(const char* key, const char* legacy) {
    unsigned id = 0;
    if (!parse_unsigned(environment(key, environment(legacy, "1000")), 4294967294U, 10, id) || id == 0) {
        throw SettingsError(std::string(legacy) + " must be an integer from 1 to 4294967294.");
    }
    return id;
}

std::string lowercase(std::string value) {
    for (auto& ch : value) {
        ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
    }
    return value;
}

bool contains_only(std::string_view value, std::string_view alphabet) {
    return !value.empty() && value.find_first_not_of(alphabet) == value.npos;
}

constexpr std::string_view alphanumerics = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";

bool valid_email(std::string_view email) {
    const auto separator = email.find('@');
    if (separator == email.npos || separator == 0 || email.find('@', separator + 1) != email.npos ||
        std::any_of(email.begin(), email.end(), [](unsigned char ch) { return std::isspace(ch) || std::iscntrl(ch); })) {
        return false;
    }
    const auto host = email.substr(separator + 1);
    return contains_only(host, std::string(alphanumerics) + ".-") &&
           !host.starts_with('.') && !host.ends_with('.') && !host.starts_with('-') &&
           !host.ends_with('-') && host.find('.') != host.npos && host.find("..") == host.npos &&
           host.find("-.") == host.npos && host.find(".-") == host.npos;
}

bool valid_resolver(std::string_view entry) {
    const auto separator = entry.rfind(':');
    unsigned port = 0;
    if (separator == entry.npos || !parse_unsigned(entry.substr(separator + 1), 65535, 5, port) || port == 0) {
        return false;
    }
    const auto host = entry.substr(0, separator);
    if (host.starts_with('[') && host.ends_with(']')) {
        in6_addr address{};
        return inet_pton(AF_INET6, std::string(host.substr(1, host.size() - 2)).c_str(), &address) == 1;
    }
    if (contains_only(host, "0123456789.") && host.find('.') != host.npos) {
        in_addr address{};
        return inet_pton(AF_INET, std::string(host).c_str(), &address) == 1;
    }
    return contains_only(host, std::string(alphanumerics) + "_.-");
}

bool parse_resolvers(std::string_view entries, std::vector<std::string>& resolvers) {
    constexpr auto whitespace = " \t\n\v\f\r";
    for (;;) {
        const auto separator = entries.find(',');
        auto item = entries.substr(0, separator);
        const auto first = item.find_first_not_of(whitespace);
        if (first == item.npos) {
            return false;
        }
        item = item.substr(first, item.find_last_not_of(whitespace) - first + 1);
        if (!valid_resolver(item)) {
            return false;
        }
        resolvers.emplace_back(item);
        if (separator == entries.npos) {
            return true;
        }
        entries.remove_prefix(separator + 1);
    }
}
}

std::string environment(const char* name, std::string fallback) {
    const char* value = std::getenv(name);
    return value && *value ? std::string(value) : fallback;
}

void set_environment(const char* name, const std::string& value) {
    if (setenv(name, value.c_str(), 1) != 0) {
        throw std::system_error(errno, std::generic_category(), "Cannot configure process environment");
    }
}

void log_message(std::ostream& stream, const char* style, std::string_view icon, std::string_view message) {
    const auto terminal = environment("TERM");
    const bool color = isatty(STDOUT_FILENO) && environment("NO_COLOR").empty() && terminal != "dumb";
    stream << (color ? style : "") << icon << message << (color ? "\033[0m" : "") << '\n';
}

Identity read_identity() {
    return {static_cast<uid_t>(read_id("CERT_UID", "UID")), static_cast<gid_t>(read_id("CERT_GID", "GID"))};
}

void drop_privileges(Identity identity) {
    if (setgroups(0, nullptr) != 0 || setgid(identity.gid) != 0 || setuid(identity.uid) != 0) {
        throw std::system_error(errno, std::generic_category(), "Cannot switch to the configured UID/GID");
    }
}

Settings read_settings(bool issue) {
    Settings result;
    std::ostringstream errors;
    result.timezone = environment("TZ", "Etc/UTC");
    std::error_code zone_error;
    if (result.timezone.starts_with('/') || result.timezone.find("..") != std::string::npos ||
        !std::filesystem::is_regular_file("/usr/share/zoneinfo/" + result.timezone, zone_error)) {
        errors << "  TZ: expected an IANA timezone such as Etc/UTC or America/Santiago.\n";
    }
    result.email = environment("EMAIL");
    if ((issue || !result.email.empty()) && !valid_email(result.email)) {
        errors << "  EMAIL: enter a valid Let's Encrypt contact address.\n";
    }
    if (issue && lowercase(environment("ACCEPT_LEGO_TOS")) != "true") {
        errors << "  ACCEPT_LEGO_TOS: set to true after reviewing https://letsencrypt.org/repository/\n";
    }
    if (!parse_unsigned(environment("PROPAGATION_SECONDS", "360"), 2147483647U, 10, result.propagation)) {
        errors << "  PROPAGATION_SECONDS: expected a non-negative integer in seconds.\n";
    }
    const auto output = environment("LEGO_OUTPUT", "auto");
    if (output != "auto" && output != "pretty" && output != "raw") {
        errors << "  LEGO_OUTPUT: expected auto, pretty or raw.\n";
    }
    result.pretty = output == "pretty" || (output == "auto" && isatty(STDOUT_FILENO));
    if (!parse_resolvers(environment("DNS_RESOLVERS", "1.1.1.1:53,8.8.8.8:53,1.0.0.1:53"), result.resolvers)) {
        errors << "  DNS_RESOLVERS: expected comma-separated host:port entries (use brackets for IPv6).\n";
    }
    if (errors.tellp() > 0) {
        log_message(std::cerr, "\033[31m", "❌ ", "Environment validation failed.");
        std::cerr << errors.str();
        throw SettingsError("");
    }
    return result;
}

int configure_cloudflare(unsigned propagation, const char* secret) {
    const auto cname = lowercase(environment("LEGO_DISABLE_CNAME_SUPPORT", "true"));
    const bool disable_delegation = cname == "true" || cname == "t" || cname == "1";
    if (!disable_delegation && cname != "false" && cname != "f" && cname != "0") {
        log_message(std::cerr, "\033[31m", "❌ ", "LEGO_DISABLE_CNAME_SUPPORT must be true or false.");
        return 2;
    }
    auto token = environment("CLOUDFLARE_API_KEY", environment("CF_DNS_API_TOKEN",
                                                            environment("CLOUDFLARE_API_TOKEN")));
    bool use_file = false;
    std::error_code secret_error;
    if (token.empty() && std::filesystem::is_regular_file(secret, secret_error)) {
        std::ifstream file(secret);
        if (file) {
            // One token with optional trailing LF characters. Bound the read of a bad mount.
            constexpr std::size_t maximum_secret_size = 4096;
            token.resize(maximum_secret_size + 1);
            file.read(token.data(), token.size());
            token.resize(file.gcount());
            if (file.bad() || token.size() > maximum_secret_size) {
                log_message(std::cerr, "\033[31m", "❌ ", "Could not read the Cloudflare API token file.");
                return 2;
            }
            while (token.ends_with('\n')) {
                token.pop_back();
            }
            use_file = true;
        }
    }
    if (!contains_only(token, std::string(alphanumerics) + "_-")) {
        log_message(std::cerr, "\033[31m", "❌ ",
                    "Provide a valid Cloudflare DNS API token through the Compose secret or CLOUDFLARE_API_TOKEN.");
        return 3;
    }
    for (const auto* name : {"CLOUDFLARE_API_KEY", "CLOUDFLARE_API_KEY_FILE", "CF_API_KEY", "CF_API_KEY_FILE",
                            "CLOUDFLARE_API_TOKEN", "CF_DNS_API_TOKEN", "CF_DNS_API_TOKEN_FILE",
                            "CLOUDFLARE_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN_FILE"}) {
        unsetenv(name);
    }
    if (use_file) {
        set_environment("CF_DNS_API_TOKEN_FILE", secret);
    } else {
        set_environment("CF_DNS_API_TOKEN", token);
    }
    set_environment("CLOUDFLARE_PROPAGATION_TIMEOUT", std::to_string(propagation));
    set_environment("LEGO_DISABLE_CNAME_SUPPORT", disable_delegation ? "true" : "false");
    return 0;
}
}
