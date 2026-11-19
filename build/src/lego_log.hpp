#pragma once

#include <string>
#include <string_view>

namespace certgen {
bool starts_with(std::string_view text, std::string_view prefix);
std::string short_log_text(std::string_view text);

struct LegoLine {
    enum class Level { text, info, warning };

    explicit LegoLine(std::string_view text);
    bool fatal_header() const;
    bool domain_failure() const;

    std::string_view timestamp;
    std::string_view message;
    Level level = Level::text;
};

class LegoLog {
public:
    void observe(const LegoLine& line);
    bool failed() const;
    bool rate_limited() const;
    std::string diagnostic(int status) const;

private:
    enum class Cause { none, fatal, propagation, rejected, zone, access, token, name_limit, rate_limit };
    Cause cause_ = Cause::none;
    std::string header_;
    std::string detail_;
    std::string last_message_;

    void record_failure(std::string_view message);
};
}
