#include "core.hpp"
#include "app.hpp"

#include <fcntl.h>
#include <sys/wait.h>
#include <unistd.h>

#include <cerrno>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>

namespace certgen {
namespace {
namespace fs = std::filesystem;

bool try_image_app(std::vector<std::string> arguments, int expected, const fs::path& output = {}) {
    std::vector<char*> argv;
    argv.reserve(arguments.size() + 1);
    for (auto& argument : arguments) {
        argv.push_back(argument.data());
    }
    argv.push_back(nullptr);
    std::cout.flush();
    std::cerr.flush();
    const pid_t child = fork();
    if (child == 0) {
        const int input = open("/dev/null", O_RDONLY | O_CLOEXEC);
        if (input < 0 || dup2(input, STDIN_FILENO) < 0) {
            _exit(125);
        }
        close(input);
        if (!output.empty()) {
            const int log = open(output.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
            if (log < 0 || dup2(log, STDOUT_FILENO) < 0) {
                _exit(125);
            }
            close(log);
        }
        execv(arguments.front().c_str(), argv.data());
        _exit(127);
    }
    int status = 0;
    pid_t waited;
    do {
        waited = child < 0 ? -1 : waitpid(child, &status, 0);
    } while (waited < 0 && child >= 0 && errno == EINTR);
    return waited == child && child > 0 && WIFEXITED(status) && WEXITSTATUS(status) == expected;
}

void check(bool passed, const std::string& message) {
    if (!passed) {
        throw std::runtime_error(message);
    }
}
}

int run_image_self_test() {
    for (const auto* path : {"/bin/sh", "/bin/bash", "/bin/busybox", "/bin/ash", "/usr/bin/dash",
                             "/sbin/apk", "/sbin/su-exec", "/usr/bin/su-exec", "/usr/bin/python3",
                             "/usr/bin/g++", "/usr/bin/curl", "/usr/local/bin/entrypoint.sh"}) {
        check(fs::symlink_status(path).type() == fs::file_type::not_found,
              std::string("Unexpected runtime program: ") + path);
    }
    for (const auto* license : {"NOTICE", "lego/LICENSE", "musl/COPYRIGHT", "ca-certificates/MPL-2.0",
                                "libyaml/LICENSE"}) {
        const auto path = fs::path("/usr/share/licenses") / license;
        check(fs::is_regular_file(path) && fs::file_size(path) > 0, "Missing license: " + path.string());
    }
    char pattern[] = "/tmp/certgen-image-test.XXXXXX";
    const char* directory = mkdtemp(pattern);
    check(directory != nullptr, "Cannot create the image test directory.");
    const fs::path fixtures(directory);
    fs::permissions(fixtures, static_cast<fs::perms>(0755));
    const auto config = (fixtures / "domains.yaml").string();
    const auto invalid = (fixtures / "invalid.yaml").string();
    const auto output = (fixtures / "no-orders").string();
    const auto prepare = [](const std::string& path, const char* content) {
        std::ofstream file(path);
        file << content;
        file.close();
        check(static_cast<bool>(file), "Cannot write image test fixture: " + path);
        fs::permissions(path, fs::perms::owner_read | fs::perms::owner_write |
                              fs::perms::group_read | fs::perms::others_read);
    };
    prepare(config, "smoke: [ipv4.example.test, ipv6.example.test, alias.example.test, "
                    "\"*.alias.example.test\", planned.example.test]\n");
    prepare(invalid, "bad: [\"not a DNS name\"]\n");
    check(setenv("TZ", "America/Santiago", 1) == 0, "Cannot set image test timezone");
    const std::string executable = "/usr/local/bin/certgen";
    std::string version = environment("LEGO_VERSION");
    const std::string architecture = environment("TARGETARCH");
    check(!version.empty() && !architecture.empty(), "The image test needs the expected lego version and architecture.");
    if (version.starts_with('v')) {
        version.erase(0, 1);
    }
    const auto smoke_log = fixtures / "plan.log";
    check(try_image_app({executable, "--config", config, "--output", output, "--test"}, 0, smoke_log),
          "The built image could not run an offline staging preview.");
    const std::string expected = "lego version " + version + " linux/" + architecture;
    bool matches_version = false;
    bool passed_offline = false;
    std::ifstream plan(smoke_log);
    for (std::string line; std::getline(plan, line);) {
        std::cout << line << '\n';
        matches_version |= line == "ℹ️  " + expected;
        passed_offline |= line == "✅ Offline test passed.";
    }
    check(matches_version && passed_offline, "The offline preview did not report " + expected + ".");
    check(!fs::exists(output), "An offline preview created an output directory.");
    check(try_image_app({executable, "--config", config}, 2), "Missing TTY was not rejected.");
    check(try_image_app({executable, "--config", config, "--test", "--production"}, 2),
          "Conflicting flags were not rejected.");
    check(try_image_app({executable, "--config", invalid, "--test"}, 2),
          "Invalid DNS name was not rejected.");
    fs::remove_all(fixtures);
    return 0;
}
}
