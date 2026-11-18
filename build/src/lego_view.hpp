#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <string_view>
#include <vector>

namespace certgen {
// Turns lego's log into a short live view: one line per DNS name whose marker
// changes as lego works (🔹 waiting, ⏳ in progress, ✅ verified, ❌ failed),
// and one status line at the bottom. Names that do not fit on the screen are
// shown as permanent lines when they finish instead.
class LegoView {
public:
    LegoView(const std::string& names, std::uint64_t limit_seconds, int fd);

    void start(double now);
    void feed(std::string_view text, double now);
    void tick(double now);
    // reason: 0 lego exited, 124 time limit, 75 rate limit.
    void finish(int status, int reason, double now);

private:
    enum class State { waiting, active, verified, failed };
    struct Name {
        std::string value;
        State state = State::waiting;
        std::string label;
        std::string reason;
        std::string cleanup;
        double started = 0;
    };

    std::vector<Name> names_;
    std::map<std::string, std::size_t, std::less<>> index_;
    std::map<std::string, std::size_t> authorizations_;
    std::vector<std::string> errors_;
    std::vector<std::string> warnings_;
    std::string pending_;
    std::string activity_;
    std::size_t current_ = npos;
    std::uint64_t limit_;
    int fd_;
    bool list_ = false;
    bool in_error_ = false;
    bool issued_ = false;
    std::size_t name_width_ = 0;
    std::size_t spinner_ = 0;
    double started_ = 0;
    // lego's own clock, from the timestamp of the current log line.
    double stamp_ = 0;
    double first_stamp_ = -1;
    double last_raw_stamp_ = -1;
    double day_offset_ = 0;
    static constexpr std::size_t npos = static_cast<std::size_t>(-1);

    void line(std::string_view text, double now);
    void domain_event(std::size_t index, std::string_view message, double now);
    void error_line(std::string_view text);
    void set(std::size_t index, State state, std::string label, double now);
    std::size_t find(std::string_view name) const;
    std::size_t verified() const;
    std::string render(const Name& name, std::size_t columns) const;
    std::string footer(double now, std::size_t columns) const;
    void draw_name(std::size_t index, std::size_t columns);
    void draw_footer(double now);
    void emit(const std::string& text) const;
};
}
