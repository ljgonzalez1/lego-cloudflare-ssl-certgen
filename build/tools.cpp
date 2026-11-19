#include <sys/wait.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>
#include <cctype>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <system_error>
#include <vector>

namespace {
namespace fs = std::filesystem;

class InvalidInput : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

std::string environment(const char* name, std::string fallback = {}) {
    const char* value = std::getenv(name);
    return value && *value ? std::string(value) : fallback;
}

[[noreturn]] void fail_system(const char* message) {
    throw std::system_error(errno, std::generic_category(), message);
}

std::string run(const std::vector<std::string>& arguments, bool capture = false) {
    std::vector<char*> argv;
    for (const auto& argument : arguments) {
        argv.push_back(const_cast<char*>(argument.c_str()));
    }
    argv.push_back(nullptr);
    int output[2];
    if (capture && pipe(output) != 0) {
        fail_system("Cannot capture command output");
    }
    const auto child = fork();
    if (child < 0) {
        const int fork_error = errno;
        if (capture) {
            close(output[0]);
            close(output[1]);
        }
        errno = fork_error;
        fail_system("Cannot start build command");
    }
    if (child == 0) {
        if (capture) {
            close(output[0]);
            if (dup2(output[1], STDOUT_FILENO) < 0) {
                _exit(125);
            }
            close(output[1]);
        }
        execvp(argv[0], argv.data());
        _exit(127);
    }
    std::string content;
    if (capture) {
        close(output[1]);
        char buffer[4096];
        for (;;) {
            const auto count = read(output[0], buffer, sizeof(buffer));
            if (count == 0 || (count < 0 && errno != EINTR)) {
                break;
            }
            if (count > 0) {
                content.append(buffer, count);
            }
        }
        close(output[0]);
    }
    int status = 0;
    pid_t waited;
    do {
        waited = waitpid(child, &status, 0);
    } while (waited < 0 && errno == EINTR);
    if (waited < 0 || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
        throw std::runtime_error("Build command failed: " + arguments.front());
    }
    return content;
}

struct WorkDirectory {
    fs::path path;

    WorkDirectory() {
        char pattern[] = "/tmp/certgen-build.XXXXXX";
        const char* created = mkdtemp(pattern);
        if (!created) {
            fail_system("Cannot create a temporary build directory");
        }
        path = created;
    }
    ~WorkDirectory() {
        std::error_code ignored;
        fs::remove_all(path, ignored);
    }
};

void put_file(const fs::path& path, const std::string& contents) {
    std::ofstream file(path);
    file << contents;
    file.close();
    if (!file) {
        throw std::runtime_error("Cannot write " + path.string());
    }
}

void fetch(const std::string& url, const fs::path& destination) {
    run({"curl", "-fsSL", "--connect-timeout", "15", "--max-time", "180", "--retry", "3",
         "-o", destination.string(), "--", url});
}

std::string numeric_version(std::string version) {
    return version.starts_with('v') ? version.substr(1) : version;
}

std::string target_architecture() {
    const auto target = environment("TARGETARCH") + "/" + environment("TARGETVARIANT");
    if (target == "amd64/" || target == "amd64/v1") {
        return "amd64";
    }
    if (target == "386/") {
        return "386";
    }
    if (target == "arm64/" || target == "arm64/v8") {
        return "arm64";
    }
    if (target == "arm/v6" || target == "arm/v7") {
        return target == "arm/v6" ? "armv6" : "armv7";
    }
    throw InvalidInput("Unsupported target platform. Use linux/amd64, linux/386, linux/arm/v6, linux/arm/v7 or linux/arm64.");
}

std::string find_checksum(const fs::path& file, const std::string& archive) {
    std::ifstream checksums(file);
    for (std::string line; std::getline(checksums, line);) {
        std::istringstream entry(line);
        std::string checksum;
        std::string filename;
        if (entry >> checksum >> filename && filename == archive) {
            return checksum;
        }
    }
    return {};
}

void download_lego(const fs::path& output) {
    const auto architecture = target_architecture();
    const auto version = environment("LEGO_VERSION");
    if (version.empty()) {
        throw InvalidInput("The lego release version is required.");
    }
    const auto release = "https://github.com/go-acme/lego/releases/download/" + version;
    const auto archive = environment("LEGO_TARBALL", "lego_" + version + "_linux_" + architecture + ".tar.gz");
    if (archive.starts_with('.') || archive.starts_with('-') ||
        archive.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-") != archive.npos) {
        throw InvalidInput("LEGO_TARBALL must be a release archive filename.");
    }
    WorkDirectory work;
    auto checksum = environment("LEGO_SHA256");
    if (checksum.empty()) {
        const auto file = work.path / "checksums";
        fetch(release + "/lego_" + numeric_version(version) + "_checksums.txt", file);
        checksum = find_checksum(file, archive);
    }
    if (checksum.empty() || checksum.find_first_not_of("0123456789ABCDEFabcdef") != checksum.npos) {
        throw std::runtime_error("Missing or invalid SHA-256 for " + archive + ".");
    }
    if (checksum.size() != 64) {
        throw std::runtime_error("Invalid SHA-256 length for " + archive + ".");
    }
    for (auto& character : checksum) {
        character = static_cast<char>(std::tolower(static_cast<unsigned char>(character)));
    }
    const auto source = work.path / "lego.tar.gz";
    fetch(environment("LEGO_URL", release + "/" + archive), source);
    const auto calculated = run({"sha256sum", source.string()}, true);
    if (!calculated.starts_with(checksum + ' ')) {
        throw std::runtime_error("SHA-256 verification failed for " + archive + ".");
    }

    const auto payload = work.path / "payload";
    fs::create_directory(payload);
    run({"tar", "-xzf", source.string(), "-C", payload.string(), "lego", "LICENSE"});
    for (const auto* name : {"lego", "LICENSE"}) {
        const auto file = payload / name;
        if (!fs::is_regular_file(fs::symlink_status(file)) || fs::file_size(file) == 0) {
            throw std::runtime_error(std::string("The lego archive must contain a nonempty regular ") + name + " file.");
        }
        const auto destination = fs::symlink_status(output / name);
        if (fs::is_symlink(destination) || fs::is_directory(destination)) {
            throw std::runtime_error("Refusing to replace a link or directory at " + (output / name).string() + ".");
        }
    }
    if (fs::equivalent(payload / "lego", payload / "LICENSE")) {
        throw std::runtime_error("The lego executable and license must be distinct files.");
    }
    fs::create_directories(output);
    for (const auto* name : {"lego", "LICENSE"}) {
        fs::copy_file(payload / name, output / name, fs::copy_options::overwrite_existing);
        const auto mode = std::string_view(name) == "lego" ? static_cast<fs::perms>(0755) : static_cast<fs::perms>(0644);
        fs::permissions(output / name, mode);
    }
}

std::string package_version(const std::string& packages, const std::string& name) {
    std::istringstream list(packages);
    const auto prefix = name + "-";
    for (std::string entry; std::getline(list, entry);) {
        if (entry.starts_with(prefix) && entry.size() > prefix.size() &&
            std::isdigit(static_cast<unsigned char>(entry[prefix.size()]))) {
            return entry.substr(prefix.size());
        }
    }
    throw std::runtime_error("Cannot find the installed version of " + name + ".");
}

std::string upstream_version(const std::string& package) {
    return package.substr(0, package.rfind("-r"));
}

void write_notices(const fs::path& licenses, const fs::path& root, const std::string& version) {
    const auto target = root / "usr/share/licenses";
    const auto runtime = run({"apk", "--root", root.string(), "info", "-v"}, true);
    const auto installed = run({"apk", "info", "-v"}, true);
    const auto musl = package_version(runtime, "musl");
    const auto bundle = package_version(runtime, "ca-certificates-bundle");
    const auto yaml = package_version(installed, "yaml-static");
    std::ifstream release_file(environment("ALPINE_RELEASE_FILE", "/etc/alpine-release"));
    std::string release;
    if (!(release_file >> release) || std::count(release.begin(), release.end(), '.') < 2) {
        throw std::runtime_error("Cannot read the Alpine release.");
    }
    const auto branch = release.substr(0, release.find('.', release.find('.') + 1)) + "-stable";
    const auto aports = "https://gitlab.alpinelinux.org/alpine/aports/-/tree/" + branch + "/main";

    const auto copy_license = [&](const char* source, const char* destination) {
        const auto path = target / destination;
        fs::create_directories(path.parent_path());
        fs::copy_file(licenses / source, path, fs::copy_options::overwrite_existing);
        fs::permissions(path, static_cast<fs::perms>(0644));
    };
    copy_license("musl.txt", "musl/COPYRIGHT");
    copy_license("MPL-2.0.txt", "ca-certificates/MPL-2.0");
    copy_license("libyaml.txt", "libyaml/LICENSE");
    put_file(target / "NOTICE",
             "lego-cloudflare-certgen is MIT licensed. This image also contains the\n"
             "following third-party software, each under its own license.\n\n"
             "lego " + version + ": MIT, see lego/LICENSE\n"
             "  Source: https://github.com/go-acme/lego/tree/" + version + "\n\n"
             "musl " + musl + ": MIT, see musl/COPYRIGHT\n"
             "  Source: https://musl.libc.org/releases/musl-" + upstream_version(musl) + ".tar.gz\n"
             "  Alpine package: " + aports + "/musl\n\n"
             "ca-certificates-bundle " + bundle + ": Mozilla's CA list under MPL-2.0, see\n"
             "ca-certificates/MPL-2.0\n  Source: " + aports + "/ca-certificates\n\n"
             "libyaml " + yaml + ", statically linked into certgen: MIT, see libyaml/LICENSE\n"
             "  Source: https://github.com/yaml/libyaml/tree/" + upstream_version(yaml) + "\n"
             "  Alpine package: " + aports + "/yaml\n");
    fs::permissions(target / "NOTICE", static_cast<fs::perms>(0644));
}

void verify_debug_information(const char* executable) {
    const auto sections = run({"readelf", "-SW", executable}, true);
    for (const auto* section : {".debug_info ", ".debug_line ", ".debug_macro "}) {
        if (sections.find(section) == sections.npos) {
            throw std::runtime_error(std::string("Missing ELF debug section: ") + section);
        }
    }
}

void prepare_runtime(const fs::path& root, const fs::path& licenses) {
    for (const auto* dir : {"usr/local/bin", "etc/apk", "app", "tmp", "ssl-certs", "root"}) {
        fs::create_directories(root / dir);
    }
    fs::copy("/etc/apk/keys", root / "etc/apk/keys", fs::copy_options::recursive | fs::copy_options::copy_symlinks);
    fs::copy_file("/etc/apk/repositories", root / "etc/apk/repositories");
    run({"apk", "--root", root.string(), "--initdb", "--no-cache", "--no-scripts", "add",
         "musl", "ca-certificates-bundle", "tzdata"});
    fs::copy_file("/etc/alpine-release", root / "etc/alpine-release", fs::copy_options::overwrite_existing);
    fs::copy_file("/etc/os-release", root / "etc/os-release", fs::copy_options::overwrite_existing);
    put_file(root / "etc/passwd", "root:x:0:0:root:/root:/sbin/nologin\n"
                                 "certgen:x:1000:1000:certgen:/nonexistent:/sbin/nologin\n");
    put_file(root / "etc/group", "root:x:0:\ncertgen:x:1000:\n");
    fs::permissions(root / "tmp", static_cast<fs::perms>(01777));
    if (chown((root / "ssl-certs").c_str(), 1000, 1000) != 0) {
        fail_system("Cannot set the runtime output directory owner");
    }
    write_notices(licenses, root, environment("LEGO_VERSION"));
}
}

int main(int argc, char* argv[]) {
    try {
        const std::string_view command = argc > 1 ? argv[1] : "";
        if (command == "download" && argc == 3) {
            download_lego(argv[2]);
        } else if (command == "prepare" && argc == 4) {
            prepare_runtime(argv[2], argv[3]);
        } else if (command == "verify-debug" && argc == 3) {
            verify_debug_information(argv[2]);
        } else if (command == "notices" && argc == 5) {
            write_notices(argv[2], argv[3], argv[4]);
        } else {
            throw InvalidInput("Usage: certgen-build download OUTPUT | prepare ROOT LICENSES | notices LICENSES ROOT VERSION | verify-debug EXECUTABLE");
        }
        return 0;
    } catch (const InvalidInput& error) {
        std::cerr << error.what() << '\n';
        return 2;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
