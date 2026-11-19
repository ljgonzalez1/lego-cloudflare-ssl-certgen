#include "app.hpp"
#include "core.hpp"

#include <sys/stat.h>
#include <unistd.h>

#include <cerrno>
#include <ctime>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <sstream>
#include <system_error>

namespace certgen {

namespace {
namespace fs = std::filesystem;
constexpr auto staging_server = "https://acme-staging-v02.api.letsencrypt.org/directory";
constexpr auto production_server = "https://acme-v02.api.letsencrypt.org/directory";
constexpr auto default_output = "/ssl-certs";

enum class Action { issue, list, preview, test };
enum class Mode { menu, staging, production };

struct Options {
    Action action = Action::issue;
    Mode mode = Mode::menu;
    std::vector<std::string_view> groups;
    std::string config = "/app/domains.yaml";
    std::string output = default_output;
    bool help = false;
};

void show_usage(std::ostream& stream) {
    stream << R"(Usage: certgen [--list | --dry-run | --test | --staging | --production]
               [--only FOLDER]... [--config FILE] [--output DIRECTORY]

  --list          Validate the YAML and list certificate groups
  --dry-run       Show the planned groups, domains and output paths
  --test          Check lego and preview an offline staging run for CI
  --staging       Issue staging certificates without prompting
  --production    Issue live certificates without confirmation
  --only FOLDER   Process only this group (repeatable)
  --config FILE   Read this YAML file (default: /app/domains.yaml)
  --output DIR    Write to this directory (default: /ssl-certs)
)";
}

void show_error(std::string_view message) {
    log_message(std::cerr, "\033[31m", "❌ ", message);
}

void show_info(std::string_view message) {
    log_message(std::cout, "\033[36m", "ℹ️  ", message);
}

void show_warning(std::ostream& stream, std::string_view message) {
    log_message(stream, "\033[33m", "⚠️  ", message);
}

void show_success(std::string_view message) {
    log_message(std::cout, "\033[32m", "✅ ", message);
}

void show_section(std::string_view title) {
    std::cout << '\n';
    log_message(std::cout, "\033[1;35m", "", title);
}

Options parse_options(int argc, char* argv[]) {
    Options options;
    for (int index = 1; index < argc; ++index) {
        const std::string_view arg = argv[index];
        if (arg == "-h" || arg == "--help") {
            options.help = true;
            return options;
        }
        if (arg == "--list" || arg == "--dry-run" || arg == "--test") {
            const auto action = arg == "--list" ? Action::list : (arg == "--dry-run" ? Action::preview : Action::test);
            if (options.action != Action::issue && options.action != action) {
                throw SettingsError("--list, --dry-run and --test are mutually exclusive.");
            }
            options.action = action;
        } else if (arg == "--staging" || arg == "--production") {
            const auto mode = arg == "--staging" ? Mode::staging : Mode::production;
            if (options.mode != Mode::menu && options.mode != mode) {
                throw SettingsError("--staging and --production are mutually exclusive.");
            }
            options.mode = mode;
        } else if (arg == "--only" || arg == "--config" || arg == "--output") {
            if (index + 1 >= argc) {
                throw SettingsError(std::string(arg) + " requires a value.");
            }
            std::string_view value = argv[++index];
            if (value.empty() || value.starts_with("--")) {
                throw SettingsError(std::string(arg) + " requires a value.");
            }
            if (arg == "--only") {
                options.groups.push_back(value);
            } else if (arg == "--config") {
                options.config = value;
            } else {
                options.output = value;
            }
        } else {
            show_error("Unknown argument: " + std::string(arg));
            show_usage(std::cerr);
            throw SettingsError("");
        }
    }
    if (options.action != Action::issue && options.mode != Mode::menu) {
        throw SettingsError("--staging and --production cannot be combined with --list, --dry-run or --test.");
    }
    return options;
}

bool output_is_mounted() {
    std::ifstream mounts("/proc/self/mountinfo");
    for (std::string line; std::getline(mounts, line);) {
        std::istringstream fields(line);
        std::string field;
        if (fields >> field >> field >> field >> field >> field && field == default_output) {
            return true;
        }
    }
    return false;
}

int read_lego_version(std::string& version) {
    const auto status = run_lego({"10", "lego", "--version"}, &version);
    while (version.ends_with('\n')) {
        version.pop_back();
    }
    return status;
}

int prepare_issuance(const Options& options, const Settings& settings, Identity identity, int& result) {
    const int credentials = configure_cloudflare(settings.propagation, "/run/secrets/cloudflare_api_token");
    if (credentials == 3) {
        result = 1;
        return 2;
    }
    if (credentials != 0) {
        return credentials;
    }
    std::error_code path_error;
    if (!fs::is_directory(options.output, path_error) || access(options.output.c_str(), W_OK | X_OK) != 0) {
        show_error("Output directory must be writable by UID " + std::to_string(identity.uid) + " and GID " +
                   std::to_string(identity.gid) + ": " + options.output);
        return 1;
    }
    const auto path = environment("PATH", "/bin:/usr/bin");
    std::string_view remaining = path;
    for (;;) {
        const auto end = remaining.find(':');
        const auto dir = remaining.substr(0, end);
        const auto candidate = fs::path(dir.empty() ? "." : dir) / "lego";
        if (fs::is_regular_file(candidate, path_error) && access(candidate.c_str(), X_OK) == 0) {
            return 0;
        }
        if (end == remaining.npos) {
            show_error("lego is missing. Rebuild the image.");
            return 1;
        }
        remaining.remove_prefix(end + 1);
    }
}

int run_group(const DomainGroup& group, const Options& options, const Settings& settings, const char* server) {
    const bool issue = options.action == Action::issue;
    std::string target;
    try {
        target = make_output_path(issue ? "issue" : "dry-run", options.output.c_str(), group.name.c_str());
    } catch (const std::exception& error) {
        show_error(error.what());
        return 2;
    }
    const auto limit = time_limit(group.count, settings.propagation);
    show_section("🚀 " + group.name);
    show_info("Output path: " + target);
    show_info("Time limit: " + std::to_string(limit) + "s (" + std::to_string((limit + 59) / 60) + " min)");
    show_info("Domains (" + std::to_string(group.count) + "):");
    if (!issue || !settings.pretty) {
        for (const auto& name : group.names) {
            log_message(std::cout, "\033[34m", "🔹 ", name);
        }
    }
    if (!issue || cancellation_status() != 0) {
        return 0;
    }

    std::string view;
    if (settings.pretty) {
        for (const auto& name : group.names) {
            if (!view.empty()) {
                view += ',';
            }
            view += name;
        }
    }
    auto first = group.names.front();
    if (first.starts_with("*.")) {
        first[0] = '_';
    }
    std::vector<std::string> arguments{
        "--log", target + "/lego.log", "--view", view, "--certificate", target + "/certificates/" + first,
        "--group", group.name, std::to_string(limit), "lego", "--accept-tos", "--email", settings.email,
        "--dns", "cloudflare", "--server", server, "--path", target
    };
    for (const auto& resolver : settings.resolvers) {
        arguments.insert(arguments.end(), {"--dns.resolvers", resolver});
    }
    for (const auto& name : group.names) {
        arguments.insert(arguments.end(), {"--domains", name});
    }
    arguments.emplace_back("run");
    int status;
    try {
        status = run_lego(std::move(arguments));
    } catch (const std::system_error& error) {
        show_error(group.name + ": " + error.what());
        return 1;
    }
    if (cancellation_status() != 0) {
        show_warning(std::cerr, "Certificate issuance cancelled. See " + target + "/lego.log for details.");
        return 1;
    }
    if (status == 0) {
        show_success("Certificates: " + target + "/certificates/");
        show_success("Account data: " + target + "/accounts/");
        return 0;
    }
    show_info("Full lego output: " + target + "/lego.log");
    return 1;
}

int process_groups(const std::vector<DomainGroup>& groups, const Options& options, Identity identity) {
    if (options.action == Action::list) {
        for (const auto& group : groups) {
            std::cout << group.name << ": " << group.count << " domains\n";
        }
        return 0;
    }
    const bool issue = options.action == Action::issue;
    const char* server = options.mode == Mode::production ? production_server : staging_server;
    int setup_status = 0;
    int setup_result = 2;
    Settings settings;
    try {
        settings = read_settings(issue);
        if (issue) {
            setup_status = prepare_issuance(options, settings, identity, setup_result);
        }
    } catch (const SettingsError&) {
        setup_status = 2;
    }
    if (options.action == Action::test) {
        if (setup_status != 0) {
            return setup_status;
        }
        std::string version;
        const auto status = read_lego_version(version);
        if (cancellation_status() != 0) {
            return cancellation_status();
        }
        if (status != 0) {
            show_error("Could not check the lego executable (exit " + std::to_string(status) + ").");
            if (!version.empty()) {
                std::cerr << version << '\n';
            }
            return 1;
        }
        if (!version.starts_with("lego version ")) {
            show_error("Unexpected response from lego --version.");
            return 1;
        }
        show_section("🧪 TEST MODE (offline)");
        show_info(version);
    }
    if (setup_status == 0) {
        set_environment("TZ", settings.timezone);
        tzset();
        show_section(options.mode == Mode::production ? "🚨 PRODUCTION MODE" : "🧪 STAGING MODE");
        show_info(std::string("ACME server: ") + server);
        show_info("Cloudflare DNS-01 validates each name with a temporary TXT record.");
        show_info("Timezone: " + settings.timezone);
        show_info("Target UID: " + std::to_string(identity.uid) + "  GID: " + std::to_string(identity.gid));
    }
    std::stringstream report;
    int exit_status = setup_status;
    for (const auto& group : groups) {
        if (cancellation_status() != 0) {
            return cancellation_status();
        }
        int result = setup_result;
        if (group.count > max_certificate_names) {
            result = 1;
            show_error(group.name + ": " + std::to_string(group.count) +
                       " DNS names exceed the 100-name limit. Split them into smaller certificate groups.");
        } else if (setup_status == 0) {
            result = run_group(group, options, settings, server);
        }
        if (result != 0 && exit_status == 0) {
            exit_status = 1;
        }
        report << group.name << '\t' << group.count << '\t' << result << '\n';
    }
    if (cancellation_status() != 0) {
        return cancellation_status();
    }
    if (issue) {
        show_section("📋 Certificate request summary (DNS names per certificate)");
        write_summary(report);
    }
    if (options.action == Action::test && exit_status == 0) {
        show_success("Offline test passed.");
    }
    return exit_status;
}
}

int run_app(int argc, char* argv[]) {
    try {
        auto options = parse_options(argc, argv);
        if (options.help) {
            show_usage(std::cout);
            return 0;
        }
        const auto identity = read_identity();
        const bool root = geteuid() == 0;
        if (!root && (geteuid() != identity.uid || getegid() != identity.gid)) {
            show_error("The container user must match the configured UID/GID (" + std::to_string(identity.uid) +
                       ":" + std::to_string(identity.gid) + ").");
            return 1;
        }
        const bool issue = options.action == Action::issue;
        if (issue && options.mode == Mode::menu) {
            std::ostringstream selection;
            if (const int status = issuance_menu(selection); status != 0) {
                return status;
            }
            const auto mode = selection.str();
            if (mode == "cancel\n") {
                show_info("Certificate issuance cancelled.");
                return 0;
            }
            options.mode = mode == "production\n" ? Mode::production : Mode::staging;
        } else if (issue && options.mode == Mode::production) {
            show_warning(std::cerr, "Production requested by --production. This run submits live orders without confirmation.");
        }
        watch_termination();
        if (root) {
            if (issue && options.output == default_output) {
                if (!output_is_mounted()) {
                    show_warning(std::cout, "Mount a host directory at /ssl-certs to keep the output.");
                }
                set_output_owner(default_output, std::to_string(identity.uid).c_str(), std::to_string(identity.gid).c_str());
            }
            drop_privileges(identity);
        }
        umask(077);
        std::vector<DomainGroup> groups;
        try {
            groups = load_domains(options.config.c_str(), options.groups, issue);
        } catch (const std::exception& error) {
            std::cerr << "Error in " << options.config << ": " << error.what() << '\n';
            return 2;
        }
        return process_groups(groups, options, identity);
    } catch (const SettingsError& error) {
        if (*error.what()) {
            show_error(error.what());
        }
        return 2;
    } catch (const std::exception& error) {
        show_error(error.what());
        return 1;
    }
}
}
