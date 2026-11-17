#!/usr/bin/env python3
"""Render certificate outcomes. The grid uses ASCII cells and borders for fixed widths."""

import shutil
import sys
import textwrap


HEADINGS = ("Certificate", "Attempted", "Issued", "Failed", "Skipped", "Status")
COMPACT_HEADINGS = ("Group", "Tried", "OK", "Fail", "Skip", "Status")
STATUSES = ("ISSUED", "FAILED", "SKIPPED")
STATUS_COLORS = {"ISSUED": "32", "FAILED": "31", "SKIPPED": "33"}


def paint(text, style):
    return f"\x1b[{style}m{text}\x1b[0m" if style else text


def render_vertical(rows, columns, color):
    """Use a one-column grid when the terminal is too narrow for six columns."""
    width = max(1, columns - 4)
    rule = paint("+" + "-" * (width + 2) + "+", "36" if color else "")
    print(rule)
    for name, *values in rows:
        cells = [name, *(f"{label} {value}" for label, value in zip(HEADINGS[1:], values) if value)]
        for cell in cells:
            for part in textwrap.wrap(cell, width, break_on_hyphens=False):
                style = STATUS_COLORS.get(values[-1], "") if color and cell.startswith("Status ") else ""
                print(f"| {paint(part.ljust(width), style)} |")
        print(rule)


def render_grid(rows, columns, color):
    for headings in (HEADINGS, COMPACT_HEADINGS):
        widths = [max(map(len, column)) for column in zip(headings, *rows)]
        overhead = sum(widths[1:]) + 3 * len(headings) + 1
        if columns is None or columns >= overhead + len(headings[0]):
            break
    else:
        render_vertical(rows, columns, color)
        return

    if columns is not None:
        widths[0] = min(widths[0], columns - overhead)
    rule = "+" + "+".join("-" * (width + 2) for width in widths) + "+"
    rule = paint(rule, "36" if color else "")

    def print_row(row, emphasis=False):
        name, *values = row
        for start in range(0, len(name), widths[0]):
            cells = [name[start:start + widths[0]], *(values if start == 0 else [""] * len(values))]
            padded = [cell.rjust(width) if 0 < index < len(cells) - 1 else cell.ljust(width)
                      for index, (cell, width) in enumerate(zip(cells, widths))]
            if color:
                padded[-1] = paint(padded[-1], STATUS_COLORS.get(cells[-1], ""))
            line = "| " + " | ".join(padded) + " |"
            print(paint(line, "1" if emphasis and color else ""))

    print(rule)
    print_row(headings, emphasis=True)
    print(rule)
    for row in rows[:-1]:
        print_row(row)
    print(rule)
    print_row(rows[-1], emphasis=True)
    print(rule)


def main():
    rows = []
    certificates = [0, 0, 0]
    totals = [0, 0, 0, 0]
    for line in sys.stdin:
        name, count_text, result_text = line.rstrip("\n").split("\t")
        count, result = int(count_text), int(result_text)
        # A certificate order covers every name in the group when it succeeds.
        if result == 0:
            counts = (count, count, 0, 0)
        elif result == 1:
            counts = (count, 0, count, 0)
        else:
            counts = (0, 0, 0, count)
        certificates[result] += 1
        totals = [left + right for left, right in zip(totals, counts)]
        rows.append((name, *map(str, counts), STATUSES[result]))
    rows.append(("TOTAL", *map(str, totals), ""))

    color = sys.stdout.isatty()
    columns = shutil.get_terminal_size().columns if color else None
    render_grid(rows, columns, color)
    issued, failed, skipped = certificates
    print(f"Certificates: {issued + failed} attempted, {issued} issued, {failed} failed, {skipped} skipped.")


if __name__ == "__main__":
    main()
