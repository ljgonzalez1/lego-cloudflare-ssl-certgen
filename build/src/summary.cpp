#include "core.hpp"

#include <sys/ioctl.h>
#include <unistd.h>

#include <algorithm>
#include <array>
#include <cstdlib>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace certgen {
namespace {
using Row = std::array<std::string, 6>;
using Widths = std::array<std::size_t, 6>;
using Counts = std::array<std::size_t, 4>;

const Row headings{"Certificate", "Attempted", "Issued", "Failed", "Skipped", "Status"};
const Row compact_headings{"Group", "Tried", "OK", "Fail", "Skip", "Status"};
constexpr std::array statuses{"ISSUED", "FAILED", "SKIPPED"};
constexpr std::array colors{"32", "31", "33"};

std::size_t terminal_columns() {
    if (const auto* setting = std::getenv("COLUMNS")) {
        try {
            if (const auto columns = read_number(setting)) {
                return columns;
            }
        } catch (const std::runtime_error&) {
        }
    }
    winsize size{};
    return ioctl(STDOUT_FILENO, TIOCGWINSZ, &size) == 0 && size.ws_col ? size.ws_col : 80;
}

std::string paint(std::string text, std::string_view style) {
    if (!style.empty()) {
        return "\033[" + std::string(style) + 'm' + text + "\033[0m";
    }
    return text;
}

std::string_view status_color(std::string_view status, bool color) {
    if (color) {
        for (std::size_t index = 0; index < statuses.size(); ++index) {
            if (status == statuses[index]) {
                return colors[index];
            }
        }
    }
    return {};
}

std::string padded(std::string_view text, std::size_t width, bool right = false) {
    const std::string space(width - text.size(), ' ');
    return right ? space + std::string(text) : std::string(text) + space;
}

void vertical(const std::vector<Row>& rows, std::size_t columns, bool color) {
    const auto width = columns > 4 ? columns - 4 : 1;
    const auto rule = paint("+" + std::string(width + 2, '-') + "+", color ? "36" : "");
    std::cout << rule << '\n';
    for (const auto& row : rows) {
        for (std::size_t index = 0; index < row.size(); ++index) {
            if (row[index].empty()) {
                continue;
            }
            const auto cell = index == 0 ? row[index] : headings[index] + ' ' + row[index];
            for (std::size_t start = 0; start < cell.size(); start += width) {
                const auto part = std::string_view(cell).substr(start, width);
                const auto style = index == row.size() - 1 ? status_color(row[index], color) : "";
                std::cout << "| " << paint(padded(part, width), style) << " |\n";
            }
        }
        std::cout << rule << '\n';
    }
}

void grid(const std::vector<Row>& rows, std::size_t columns, bool color) {
    Widths widths{};
    const Row* header = nullptr;
    for (const Row* candidate : {&headings, &compact_headings}) {
        std::size_t overhead = 3 * candidate->size() + 1;
        for (std::size_t index = 0; index < widths.size(); ++index) {
            widths[index] = (*candidate)[index].size();
            for (const auto& row : rows) {
                widths[index] = std::max(widths[index], row[index].size());
            }
            if (index > 0) {
                overhead += widths[index];
            }
        }
        if (columns == 0 || columns >= overhead + (*candidate)[0].size()) {
            header = candidate;
            if (columns != 0) {
                widths[0] = std::min(widths[0], columns - overhead);
            }
            break;
        }
    }
    if (!header) {
        vertical(rows, columns, color);
        return;
    }

    std::string border = "+";
    for (const auto width : widths) {
        border += std::string(width + 2, '-') + '+';
    }
    const auto rule = paint(border, color ? "36" : "");
    const auto print_row = [&widths, color](const Row& row, bool emphasis = false) {
        for (std::size_t start = 0; start < row[0].size(); start += widths[0]) {
            std::string line = "| " + padded(std::string_view(row[0]).substr(start, widths[0]), widths[0]);
            for (std::size_t index = 1; index < row.size(); ++index) {
                const bool status = index == row.size() - 1;
                const std::string_view cell = start == 0 ? std::string_view(row[index]) : "";
                line += " | " + paint(padded(cell, widths[index], !status), status ? status_color(cell, color) : "");
            }
            std::cout << paint(line + " |", emphasis && color ? "1" : "") << '\n';
        }
    };

    std::cout << rule << '\n';
    print_row(*header, true);
    std::cout << rule << '\n';
    for (std::size_t index = 0; index + 1 < rows.size(); ++index) {
        print_row(rows[index]);
    }
    std::cout << rule << '\n';
    print_row(rows.back(), true);
    std::cout << rule << '\n';
}
}

void write_summary() {
    std::vector<Row> rows;
    std::array<std::size_t, 3> certificates{};
    Counts totals{};
    for (std::string line; std::getline(std::cin, line);) {
        const std::string_view fields(line);
        const auto first = fields.find('\t');
        const auto second = fields.find('\t', first == fields.npos ? first : first + 1);
        if (second == fields.npos || !valid_group(fields.substr(0, first))) {
            throw std::runtime_error("expected group, count and result separated by tabs");
        }
        const auto count = read_number(fields.substr(first + 1, second - first - 1));
        const auto result = read_number(fields.substr(second + 1));
        if (count == 0 || (count > max_certificate_names && result == 0) || result >= certificates.size()) {
            throw std::runtime_error("invalid domain count or certificate result");
        }
        const Counts amounts{result == 2 ? 0 : count, result == 0 ? count : 0,
                             result == 1 ? count : 0, result == 2 ? count : 0};
        ++certificates[result];
        Row row{std::string(fields.substr(0, first)), "", "", "", "", statuses[result]};
        for (std::size_t index = 0; index < amounts.size(); ++index) {
            totals[index] += amounts[index];
            row[index + 1] = std::to_string(amounts[index]);
        }
        rows.push_back(std::move(row));
    }
    if (std::cin.bad()) {
        throw std::runtime_error("could not read report input");
    }
    rows.push_back({"TOTAL", std::to_string(totals[0]), std::to_string(totals[1]),
                    std::to_string(totals[2]), std::to_string(totals[3]), ""});

    const bool color = isatty(STDOUT_FILENO);
    grid(rows, color ? terminal_columns() : 0, color);
    const auto [issued, failed, skipped] = certificates;
    std::cout << "Certificates: " << issued + failed << " attempted, " << issued << " issued, "
              << failed << " failed, " << skipped << " skipped.\n";
}
}
