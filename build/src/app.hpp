#pragma once

#include <iosfwd>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

#include <sys/types.h>

namespace certgen {
struct Identity {
    uid_t uid;
    gid_t gid;
};

struct Settings {
    std::string timezone;
    std::string email;
    unsigned propagation = 0;
    bool pretty = false;
    std::vector<std::string> resolvers;
};

class SettingsError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

std::string environment(const char* name, std::string fallback = {});
void set_environment(const char* name, const std::string& value);
void log_message(std::ostream& stream, const char* style, std::string_view icon, std::string_view message);
Identity read_identity();
void drop_privileges(Identity identity);
Settings read_settings(bool issue);
int configure_cloudflare(unsigned propagation, const char* secret);
}
