#pragma once

#include <cstddef>
#include <string_view>

namespace certgen {
bool valid_group(std::string_view name);
std::size_t read_number(std::string_view text);
void write_domains(int argc, char* argv[]);
void write_summary();
void write_identity();
void set_output_owner(const char* path, const char* uid, const char* gid);
void write_output_path(const char* mode, const char* root, const char* group);
}
