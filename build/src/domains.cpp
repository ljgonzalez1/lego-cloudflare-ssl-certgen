#include "core.hpp"

#include <yaml.h>

#include <algorithm>
#include <array>
#include <cstdio>
#include <initializer_list>
#include <iostream>
#include <memory>
#include <set>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <utility>
#include <variant>
#include <vector>

namespace certgen {
namespace {
bool digit(char character) {
    return character >= '0' && character <= '9';
}

bool alphanumeric(char character) {
    return digit(character) || (character >= 'a' && character <= 'z') ||
           (character >= 'A' && character <= 'Z');
}

std::string lowercase(std::string text) {
    for (auto& character : text) {
        if (character >= 'A' && character <= 'Z') {
            character = static_cast<char>(character - 'A' + 'a');
        }
    }
    return text;
}

std::string visible(std::string_view value) {
    std::string result;
    for (const auto character : value.substr(0, 253)) {
        result += character >= ' ' && character <= '~' ? character : '?';
    }
    if (value.size() > 253) {
        result += "...";
    }
    return result.empty() ? "(empty)" : result;
}

bool valid_domain(std::string_view name) {
    if (name.size() > 253) {
        return false;
    }
    if (name.substr(0, 2) == "*.") {
        name.remove_prefix(2);
    }
    const auto last_dot = name.rfind('.');
    if (last_dot == std::string_view::npos) {
        return false;
    }
    const auto suffix = name.substr(last_dot + 1);
    if (std::all_of(suffix.begin(), suffix.end(), digit)) {
        return false;
    }
    for (;;) {
        const auto dot = name.find('.');
        const auto label = name.substr(0, dot);
        if (label.empty() || label.size() > 63 || !alphanumeric(label.front()) ||
            !alphanumeric(label.back()) ||
            !std::all_of(label.begin(), label.end(), [](char character) {
                return alphanumeric(character) || character == '-';
            })) {
            return false;
        }
        if (dot == std::string_view::npos) {
            return true;
        }
        name.remove_prefix(dot + 1);
    }
}

// A YAML 1.1 decimal with a signed exponent can also resemble a DNS name.
bool plain_decimal(std::string_view name) {
    const auto dot = name.find('.');
    const auto exponent = name.find_first_of("eE", dot + 1);
    return dot != std::string_view::npos && exponent != std::string_view::npos &&
           exponent + 2 < name.size() && name[exponent + 1] == '-' &&
           std::all_of(name.begin(), name.begin() + dot, digit) &&
           std::all_of(name.begin() + dot + 1, name.begin() + exponent, digit) &&
           std::all_of(name.begin() + exponent + 2, name.end(), digit);
}

// Keep the YAML 1.1 scalar behavior relevant to the folder grammar. Quoted
// strings and explicitly tagged strings may use names such as "yes" or "42".
bool plain_folder_string(std::string_view name) {
    constexpr std::array reserved{
        "yes", "Yes", "YES", "no", "No", "NO", "true", "True", "TRUE",
        "false", "False", "FALSE", "on", "On", "ON", "off", "Off", "OFF",
        "null", "Null", "NULL",
    };
    if (std::find(reserved.begin(), reserved.end(), name) != reserved.end()) {
        return false;
    }
    const auto allowed_digits = [](std::string_view value, std::string_view alphabet) {
        return !value.empty() && value.find_first_not_of(alphabet) == std::string_view::npos;
    };
    if (name.front() >= '1' && name.front() <= '9' && allowed_digits(name, "0123456789_")) {
        return false;
    }
    if (name == "0" || (name.front() == '0' && allowed_digits(name, "01234567_")) ||
        (name.substr(0, 2) == "0x" && allowed_digits(name.substr(2), "0123456789abcdefABCDEF_")) ||
        (name.substr(0, 2) == "0b" && allowed_digits(name.substr(2), "01_"))) {
        return false;
    }
    return !(name.size() == 10 && name[4] == '-' && name[7] == '-' &&
             allowed_digits(name.substr(0, 4), "0123456789") &&
             allowed_digits(name.substr(5, 2), "0123456789") &&
             allowed_digits(name.substr(8, 2), "0123456789"));
}

std::string yaml_string(const yaml_char_t* text) {
    return text ? reinterpret_cast<const char*>(text) : "";
}

void check_tag(const yaml_char_t* tag, std::string_view expected) {
    if (tag) {
        const auto name = yaml_string(tag);
        if (name != "!" && name != expected) {
            throw std::runtime_error("unexpected YAML type: " + visible(name));
        }
    }
}

class Parser {
public:
    explicit Parser(FILE* file) {
        if (!yaml_parser_initialize(&parser_)) {
            throw std::runtime_error("could not initialize YAML parser");
        }
        yaml_parser_set_input_file(&parser_, file);
    }

    ~Parser() {
        yaml_event_delete(&event_);
        yaml_parser_delete(&parser_);
    }

    Parser(const Parser&) = delete;
    Parser& operator=(const Parser&) = delete;

    void next() {
        yaml_event_delete(&event_);
        event_ = {};
        if (!yaml_parser_parse(&parser_, &event_)) {
            const std::string message = parser_.problem ? parser_.problem : "invalid YAML";
            throw std::runtime_error("YAML line " + std::to_string(parser_.problem_mark.line + 1) +
                                     ": " + visible(message));
        }
    }

    const yaml_event_t& event() const {
        return event_;
    }

    void expect(yaml_event_type_t type, const char* message) const {
        if (event_.type != type) {
            throw std::runtime_error(message);
        }
    }

private:
    yaml_parser_t parser_{};
    yaml_event_t event_{};
};

struct Scalar {
    std::string value;
    bool implicitly_typed;
};

using Domains = std::vector<Scalar>;
using AnchoredValue = std::variant<Scalar, Domains>;
struct Group {
    std::string name;
    Domains domains;
};

class Configuration {
public:
    explicit Configuration(FILE* file) : parser_(file) {}

    std::vector<Group> read() {
        parser_.next();
        parser_.expect(YAML_STREAM_START_EVENT, "expected a YAML stream");
        parser_.next();
        parser_.expect(YAML_DOCUMENT_START_EVENT, "expected a nonempty mapping of certificate folders to domain lists");
        parser_.next();
        parser_.expect(YAML_MAPPING_START_EVENT, "expected a nonempty mapping of certificate folders to domain lists");
        check_tag(parser_.event().data.mapping_start.tag, YAML_MAP_TAG);
        parser_.next();

        std::vector<Group> groups;
        std::set<std::string> names;
        while (parser_.event().type != YAML_MAPPING_END_EVENT) {
            auto key = scalar("invalid certificate folder");
            if (!valid_group(key.value) || (key.implicitly_typed && !plain_folder_string(key.value))) {
                throw std::runtime_error("invalid certificate folder: " + visible(key.value));
            }
            if (!names.insert(key.value).second) {
                throw std::runtime_error("duplicate certificate folder: " + key.value);
            }
            groups.push_back({key.value, domains(key.value)});
        }
        if (groups.empty()) {
            throw std::runtime_error("expected a nonempty mapping of certificate folders to domain lists");
        }
        parser_.next();
        parser_.expect(YAML_DOCUMENT_END_EVENT, "expected the end of the YAML document");
        parser_.next();
        parser_.expect(YAML_STREAM_END_EVENT, "expected a single YAML document");
        return groups;
    }

private:
    Parser parser_;
    std::unordered_map<std::string, AnchoredValue> anchors_;

    void remember(const std::string& anchor, AnchoredValue value) {
        if (!anchor.empty() && !anchors_.emplace(anchor, std::move(value)).second) {
            throw std::runtime_error("duplicate YAML anchor: " + visible(anchor));
        }
    }

    template <typename Value>
    Value alias(const std::string& expected) {
        const auto anchor = yaml_string(parser_.event().data.alias.anchor);
        const auto found = anchors_.find(anchor);
        if (found == anchors_.end()) {
            throw std::runtime_error("undefined or recursive YAML alias: " + visible(anchor));
        }
        const auto* value = std::get_if<Value>(&found->second);
        if (!value) {
            throw std::runtime_error(expected);
        }
        parser_.next();
        return *value;
    }

    Scalar scalar(const std::string& expected) {
        if (parser_.event().type == YAML_ALIAS_EVENT) {
            return alias<Scalar>(expected);
        }
        parser_.expect(YAML_SCALAR_EVENT, expected.c_str());
        const auto& data = parser_.event().data.scalar;
        check_tag(data.tag, YAML_STR_TAG);
        Scalar value{
            std::string(reinterpret_cast<const char*>(data.value), data.length),
            (data.tag == nullptr && data.style == YAML_PLAIN_SCALAR_STYLE) ||
                yaml_string(data.tag) == "!",
        };
        if (data.anchor) {
            remember(yaml_string(data.anchor), value);
        }
        parser_.next();
        return value;
    }

    Domains domains(const std::string& group) {
        const auto expected = group + ": expected a nonempty domain list";
        Domains values;
        std::string anchor;
        if (parser_.event().type == YAML_ALIAS_EVENT) {
            values = alias<Domains>(expected);
        } else {
            parser_.expect(YAML_SEQUENCE_START_EVENT, expected.c_str());
            check_tag(parser_.event().data.sequence_start.tag, YAML_SEQ_TAG);
            anchor = yaml_string(parser_.event().data.sequence_start.anchor);
            parser_.next();
            while (parser_.event().type != YAML_SEQUENCE_END_EVENT) {
                values.push_back(scalar(group + ": invalid DNS name: expected a string"));
            }
            parser_.next();
        }
        if (values.empty()) {
            throw std::runtime_error(expected);
        }
        std::set<std::string> seen;
        for (auto& value : values) {
            // DNS permits a final root dot. ACME and filenames use the undotted form.
            if (!value.value.empty() && value.value.back() == '.') {
                value.value.pop_back();
            }
            if (!valid_domain(value.value) || (value.implicitly_typed && plain_decimal(value.value))) {
                throw std::runtime_error(group + ": invalid DNS name: " + visible(value.value));
            }
            if (!seen.insert(lowercase(value.value)).second) {
                throw std::runtime_error(group + ": duplicate DNS name: " + value.value);
            }
        }
        if (!anchor.empty()) {
            remember(anchor, values);
        }
        return values;
    }
};
}

bool valid_group(std::string_view name) {
    return !name.empty() && name.size() <= 100 && alphanumeric(name.front()) &&
           std::all_of(name.begin(), name.end(), [](char character) {
               return alphanumeric(character) || character == '-' || character == '_';
           });
}

void write_domains(int argc, char* argv[], bool for_issuance) {
    const auto close_file = [](FILE* handle) { std::fclose(handle); };
    const std::unique_ptr<FILE, decltype(close_file)> file(std::fopen(argv[0], "rb"), close_file);
    if (!file) {
        throw std::runtime_error("could not open configuration");
    }
    auto groups = Configuration(file.get()).read();
    if (!for_issuance) {
        for (const auto& group : groups) {
            if (group.domains.size() > max_certificate_names) {
                throw std::runtime_error(group.name + ": identifiers exceed the 100-name limit");
            }
        }
    }
    const std::set<std::string_view> selected(argv + 1, argv + argc);
    std::string unknown;
    std::set<std::string_view> reported;
    for (int index = 1; index < argc; ++index) {
        const std::string_view name = argv[index];
        if (std::none_of(groups.begin(), groups.end(), [name](const Group& group) {
                return group.name == name;
            }) && reported.insert(name).second) {
            if (!unknown.empty()) {
                unknown += ", ";
            }
            unknown += visible(name);
        }
    }
    if (!unknown.empty()) {
        throw std::runtime_error("unknown certificate folder(s): " + unknown);
    }
    for (const auto& group : groups) {
        if (selected.empty() || selected.count(group.name)) {
            std::cout << group.name << '\t';
            if (group.domains.size() > max_certificate_names) {
                // Report the rejected group's size without forwarding the names to lego.
                std::cout << "-\t" << group.domains.size() << '\n';
                continue;
            }
            const char* separator = "";
            for (const auto& domain : group.domains) {
                std::cout << separator << lowercase(domain.value);
                separator = ",";
            }
            std::cout << '\n';
        }
    }
}
}
