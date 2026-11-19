#include "core.hpp"

#include <sys/stat.h>
#include <unistd.h>

#include <cerrno>
#include <ctime>
#include <filesystem>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <string>
#include <system_error>

namespace certgen {
namespace {
namespace fs = std::filesystem;

[[noreturn]] void os_error(const std::string& message) {
    throw std::system_error(errno, std::generic_category(), message);
}

template <typename Id>
Id checked_id(const char* text) {
    const auto id = read_number(text);
    if (id == 0 || id >= std::numeric_limits<Id>::max()) {
        throw std::runtime_error("invalid UID or GID");
    }
    return static_cast<Id>(id);
}

std::string timestamp(std::time_t epoch) {
    std::tm local{};
    char formatted[64];
    if (!localtime_r(&epoch, &local) ||
        std::strftime(formatted, sizeof(formatted), "%Y-%m-%d_%H.%M.%S %z", &local) == 0) {
        throw std::runtime_error("could not format local time");
    }
    const std::string text(formatted);
    const auto space = text.find(' ');
    if (space == std::string::npos || text.size() - space < 6) {
        throw std::runtime_error("could not find the UTC offset");
    }
    const auto offset = text.substr(space + 1);
    const auto hours = offset[1] == '0' ? offset.substr(2, 1) : offset.substr(1, 2);
    return text.substr(0, space) + "_GMT" + offset[0] + hours;
}

std::string reserve_timestamp(const fs::path& parent, std::time_t& epoch) {
    if (fs::is_symlink(fs::symlink_status(parent))) {
        throw std::runtime_error("Cannot use certificate folder: " + parent.string());
    }
    fs::create_directories(parent);
    auto stamp = timestamp(epoch);
    for (;;) {
        const auto directory = parent / stamp;
        if (mkdir(directory.c_str(), 0700) == 0) {
            return stamp;
        }
        if (errno != EEXIST) {
            os_error("Cannot create timestamp folder: " + directory.string());
        }
        stamp = timestamp(++epoch);
    }
}
}

void write_identity() {
    std::cout << geteuid() << ':' << getegid() << '\n';
}

void set_output_owner(const char* path, const char* uid, const char* gid) {
    const auto owner = checked_id<uid_t>(uid);
    const auto group = checked_id<gid_t>(gid);
    if (chown(path, owner, group) != 0) {
        os_error("Cannot set output ownership");
    }
}

std::string make_output_path(const char* mode, const char* root, const char* group) {
    const std::string_view action(mode);
    if ((action != "issue" && action != "dry-run") || !valid_group(group)) {
        throw std::runtime_error("invalid output path request");
    }
    auto epoch = std::time(nullptr);
    if (epoch == static_cast<std::time_t>(-1)) {
        os_error("Cannot read the current time");
    }
    const auto parent = fs::path(root) / group;
    const auto stamp = action == "issue" ? reserve_timestamp(parent, epoch) : timestamp(epoch);
    const auto requested = timestamp(epoch + 55);
    const auto expires = timestamp(epoch + 90 * 86400 - 12 * 3600);
    const auto target = parent / stamp / ("requested_" + requested + "_expires_" + expires);
    if (action == "issue" && mkdir(target.c_str(), 0700) != 0) {
        os_error("Cannot create certificate folder: " + target.string());
    }
    return target.string();
}

void write_output_path(const char* mode, const char* root, const char* group) {
    std::cout << make_output_path(mode, root, group) << '\n';
}
}
