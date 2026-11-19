package certgen

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type ReportRow struct {
	Group         string
	Count, Result int
}

func paint(text, style string, color bool) string {
	if color && style != "" {
		return "\x1b[" + style + "m" + text + reset
	}
	return text
}
func pad(text string, width int, right bool) string {
	space := strings.Repeat(" ", max(0, width-len(text)))
	if right {
		return space + text
	}
	return text + space
}
func statusColor(text string) string {
	switch text {
	case "ISSUED":
		return "32"
	case "FAILED":
		return "31"
	case "SKIPPED":
		return "33"
	}
	return ""
}
func WriteSummary(output *os.File, report []ReportRow) error {
	headings := []string{"Certificate", "Attempted", "Issued", "Failed", "Skipped", "Status"}
	compact := []string{"Group", "Tried", "OK", "Fail", "Skip", "Status"}
	statuses := []string{"ISSUED", "FAILED", "SKIPPED"}
	var rows [][6]string
	var certificates [3]int
	var totals [4]uint64
	for _, entry := range report {
		if !validGroup(entry.Group) || entry.Count <= 0 || entry.Result < 0 || entry.Result > 2 || entry.Count > 100 && entry.Result == 0 {
			return fmt.Errorf("invalid domain count or certificate result")
		}
		var amounts [4]uint64
		if entry.Result == 2 {
			amounts[3] = uint64(entry.Count)
		} else {
			amounts[0] = uint64(entry.Count)
			amounts[entry.Result+1] = uint64(entry.Count)
		}
		certificates[entry.Result]++
		row := [6]string{entry.Group, "", "", "", "", statuses[entry.Result]}
		for i, value := range amounts {
			totals[i] += value
			row[i+1] = strconv.FormatUint(value, 10)
		}
		rows = append(rows, row)
	}
	total := [6]string{"TOTAL", "", "", "", "", ""}
	for i, value := range totals {
		total[i+1] = strconv.FormatUint(value, 10)
	}
	rows = append(rows, total)
	color := isTerminal(output) && os.Getenv("NO_COLOR") == ""
	columns := 0
	if isTerminal(output) {
		_, columns = terminalSize(output)
	}
	var widths [6]int
	var header []string
	for _, candidate := range [][]string{headings, compact} {
		overhead := 19
		for i, label := range candidate {
			widths[i] = len(label)
			for _, row := range rows {
				widths[i] = max(widths[i], len(row[i]))
			}
			if i > 0 {
				overhead += widths[i]
			}
		}
		if columns == 0 || columns >= overhead+len(candidate[0]) {
			header = candidate
			if columns > 0 {
				widths[0] = min(widths[0], columns-overhead)
			}
			break
		}
	}
	var builder strings.Builder
	if header == nil {
		width := max(1, columns-4)
		rule := paint("+"+strings.Repeat("-", width+2)+"+", "36", color)
		fmt.Fprintln(&builder, rule)
		for _, row := range rows {
			for i, value := range row {
				if value == "" {
					continue
				}
				cell := value
				if i > 0 {
					cell = headings[i] + " " + value
				}
				for len(cell) > 0 {
					size := min(width, len(cell))
					style := ""
					if i == 5 {
						style = statusColor(value)
					}
					fmt.Fprintf(&builder, "| %s |\n", paint(pad(cell[:size], width, false), style, color))
					cell = cell[size:]
				}
			}
			fmt.Fprintln(&builder, rule)
		}
	} else {
		var border strings.Builder
		border.WriteByte('+')
		for _, width := range widths {
			border.WriteString(strings.Repeat("-", width+2) + "+")
		}
		rule := paint(border.String(), "36", color)
		writeRow := func(row [6]string, emphasis bool) {
			label := row[0]
			first := true
			for len(label) > 0 {
				size := min(widths[0], len(label))
				line := "| " + pad(label[:size], widths[0], false)
				for i := 1; i < 6; i++ {
					cell := ""
					if first {
						cell = row[i]
					}
					style := ""
					if i == 5 {
						style = statusColor(cell)
					}
					line += " | " + paint(pad(cell, widths[i], i != 5), style, color)
				}
				style := ""
				if emphasis {
					style = "1"
				}
				fmt.Fprintln(&builder, paint(line+" |", style, color))
				label = label[size:]
				first = false
			}
		}
		fmt.Fprintln(&builder, rule)
		var title [6]string
		copy(title[:], header)
		writeRow(title, true)
		fmt.Fprintln(&builder, rule)
		for _, row := range rows[:len(rows)-1] {
			writeRow(row, false)
		}
		fmt.Fprintln(&builder, rule)
		writeRow(rows[len(rows)-1], true)
		fmt.Fprintln(&builder, rule)
	}
	fmt.Fprintf(&builder, "Certificates: %d attempted, %d issued, %d failed, %d skipped.\n", certificates[0]+certificates[1], certificates[0], certificates[1], certificates[2])
	_, err := io.WriteString(output, builder.String())
	return err
}
func ReadReport(input io.Reader) ([]ReportRow, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), 16384)
	var report []ReportRow
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("expected group, count and result separated by tabs")
		}
		count, err := uintSetting(fields[1], 10, 2147483647)
		if err != nil {
			return nil, err
		}
		result, err := uintSetting(fields[2], 1, 2)
		if err != nil {
			return nil, err
		}
		report = append(report, ReportRow{fields[0], int(count), int(result)})
		if len(report) > maxConfigurationNodes {
			return nil, fmt.Errorf("too many report rows")
		}
	}
	return report, scanner.Err()
}
