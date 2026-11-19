#pragma once

#include <cstddef>
#include <cstdint>
#include <iosfwd>
#include <string>
#include <string_view>
#include <vector>

namespace certgen {
inline constexpr std::size_t max_certificate_names = 100;

bool valid_group(std::string_view name);
std::size_t read_number(std::string_view text);
struct DomainGroup {
    std::string name;
    std::vector<std::string> names;
    std::size_t count;
};
std::vector<DomainGroup> load_domains(const char* path, const std::vector<std::string_view>& selected,
                                     bool for_issuance);
void write_domains(int argc, char* argv[], bool for_issuance);
void write_summary(std::istream& input);
void write_identity();
int issuance_menu(std::ostream& output);
void set_output_owner(const char* path, const char* uid, const char* gid);
std::string make_output_path(const char* mode, const char* root, const char* group);
void write_output_path(const char* mode, const char* root, const char* group);
std::uint64_t time_limit(std::uint64_t names, std::uint64_t propagation);
void write_time_limit(const char* names, const char* propagation);
int supervise(int argc, char* argv[]);
int run_app(int argc, char* argv[]);
int run_image_self_test();
int run_lego(std::vector<std::string> arguments, std::string* output = nullptr);
int cancellation_status();
void watch_termination();
}
