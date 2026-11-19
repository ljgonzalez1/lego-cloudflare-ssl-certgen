#include "core.hpp"

#include <charconv>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string_view>

namespace certgen {
std::size_t read_number(std::string_view text) {
    std::size_t value = 0;
    const auto result = std::from_chars(text.data(), text.data() + text.size(), value);
    if (result.ec != std::errc{} || result.ptr != text.data() + text.size()) {
        throw std::runtime_error("expected an unsigned integer");
    }
    return value;
}
}

int main(int argc, char* argv[]) {
    const std::string_view command = argc > 1 ? argv[1] : "";
    const bool domains = command == "domains" && argc >= 3;
    const int config = domains && argc >= 4 && std::string_view(argv[2]) == "--issue" ? 3 : 2;
    try {
        if (domains) {
            certgen::write_domains(argc - config, argv + config, config == 3);
        } else if (command == "summary" && argc == 2) {
            certgen::write_summary();
        } else if (command == "identity" && argc == 2) {
            certgen::write_identity();
        } else if (command == "own-output" && argc == 5) {
            certgen::set_output_owner(argv[2], argv[3], argv[4]);
        } else if (command == "time-limit" && argc == 4) {
            certgen::write_time_limit(argv[2], argv[3]);
        } else if (command == "supervise" && argc >= 4) {
            std::cout.flush();
            return certgen::supervise(argc - 2, argv + 2);
        } else if (command == "output-path" && argc == 5) {
            certgen::write_output_path(argv[2], argv[3], argv[4]);
        } else {
            std::cerr << "Usage: certgen-core domains [--issue] FILE [GROUP...] | summary | identity"
                         " | own-output PATH UID GID | output-path ACTION DIRECTORY GROUP"
                         " | time-limit NAMES PROPAGATION_SECONDS"
                         " | supervise [--log FILE] [--view NAME,...] [--certificate STEM]"
                         " [--group GROUP] SECONDS COMMAND...\n";
            return 2;
        }
        std::cout.flush();
        if (!std::cout) {
            throw std::runtime_error("could not write output");
        }
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "Error in " << (domains ? std::string_view(argv[config]) : command)
                  << ": " << error.what() << '\n';
        return 2;
    }
}
