package certgen

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/spinner"
)

const reset = "\x1b[0m"
const clearLine = "\r\x1b[2K"

type viewName struct {
	Value, Label, Reason, Cleanup string
	State                         int
	Started                       float64
}
type LegoView struct {
	names                                 []viewName
	index                                 map[string]int
	warnings                              []string
	activity                              string
	current                               int
	limit                                 uint64
	output                                *os.File
	list, ansi, inError                   bool
	started                               time.Time
	stamp, firstStamp, lastRaw, dayOffset float64
	active                                bool
	pulse                                 string
}

func NewLegoView(names []string, limit uint64, output *os.File) *LegoView {
	v := &LegoView{index: make(map[string]int, len(names)), current: -1, limit: limit, output: output, firstStamp: -1, lastRaw: -1, ansi: isTerminal(output) && supportsANSI()}
	for _, name := range names {
		v.index[name] = len(v.names)
		v.names = append(v.names, viewName{Value: name})
	}
	return v
}
func displayWidth(ch rune) int {
	if unicode.Is(unicode.Mn, ch) || ch == 0xFE0F || ch == 0x200D {
		return 0
	}
	if ch >= 0x1F000 || ch >= 0x2300 && ch <= 0x23FF || ch >= 0x2600 && ch <= 0x27BF || ch >= 0x2E80 && ch <= 0xA4CF || ch >= 0xAC00 && ch <= 0xD7A3 || ch >= 0xF900 && ch <= 0xFAFF {
		return 2
	}
	return 1
}
func fit(text string, width int) string {
	if width < 1 {
		return ""
	}
	var output strings.Builder
	used := 0
	escape := false
	truncated := false
	for _, ch := range text {
		if ch == 27 {
			escape = true
		}
		if escape {
			output.WriteRune(ch)
			if ch == 'm' {
				escape = false
			}
			continue
		}
		size := displayWidth(ch)
		if used+size > width {
			truncated = true
			break
		}
		output.WriteRune(ch)
		used += size
	}
	if truncated && strings.Contains(output.String(), "\x1b[") {
		output.WriteString(reset)
	}
	return output.String()
}
func (v *LegoView) emit(text string) { fmt.Fprint(v.output, text) }
func (v *LegoView) paint(text, code string) string {
	if v.ansi {
		return "\x1b[" + code + "m" + text + reset
	}
	return text
}
func (v *LegoView) clear() string {
	if v.ansi {
		return clearLine
	}
	return ""
}
func (v *LegoView) verified() int {
	n := 0
	for _, name := range v.names {
		if name.State == 2 {
			n++
		}
	}
	return n
}
func duration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	n := int(seconds + 0.5)
	if n < 60 {
		return fmt.Sprintf("%ds", n)
	}
	return fmt.Sprintf("%dm%ds", n/60, n%60)
}
func clock(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	n := int(seconds)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}
func (v *LegoView) render(index int, columns int) string {
	name := v.names[index]
	markers := []string{"🔹", "⏳", "✅", "❌"}
	colors := []string{"34", "33", "32", "31"}
	width := 0
	for _, entry := range v.names {
		width = max(width, len(entry.Value))
	}
	width = min(width, max(columns/2, 16))
	marker := v.paint(markers[name.State], colors[name.State])
	gap := " "
	if v.ansi {
		// Reserve three columns for either the pulse or a two-column status icon.
		gap = "  "
		if v.active && name.State == 1 {
			marker = v.pulse
			gap = " "
		}
	}
	text := marker + gap + v.paint(name.Value, colors[name.State])
	if name.Label != "" {
		text += strings.Repeat(" ", max(0, width-len(name.Value))) + "  " + name.Label
	}
	return fit(text, columns-1)
}
func (v *LegoView) Start() {
	v.started = time.Now()
	v.active = true
	v.pulse = spinner.At(0).Render(true)
	rows, columns := terminalSize(v.output)
	v.list = v.ansi && rows >= len(v.names)+3
	v.activity = "starting lego"
	if v.list {
		for i := range v.names {
			v.emit(v.render(i, columns) + "\n")
		}
	}
	v.Tick()
}
func (v *LegoView) Tick() {
	if !v.ansi || !v.active {
		return
	}
	_, columns := terminalSize(v.output)
	elapsed := time.Since(v.started)
	frame := spinner.At(elapsed).Render(true)
	changed := frame != v.pulse
	v.pulse = frame
	if v.list && changed {
		for index, name := range v.names {
			if name.State == 1 {
				v.redraw(index, columns)
			}
		}
	}
	text := fmt.Sprintf("%s / %s · %d/%d verified · %s", clock(elapsed.Seconds()), clock(float64(v.limit)), v.verified(), len(v.names), v.activity)
	v.emit(clearLine + fit(frame+" "+v.paint(text, "36"), columns-1))
}

// Close clears an unfinished progress line, including early supervisor errors.
// No cursor visibility or terminal mode is changed by the animation.
func (v *LegoView) Close() {
	wasActive := v.active
	v.active = false
	if wasActive && v.ansi {
		if v.list {
			_, columns := terminalSize(v.output)
			for index, name := range v.names {
				if name.State == 1 {
					v.redraw(index, columns)
				}
			}
		}
		v.emit(clearLine)
	}
}

func (v *LegoView) redraw(index, columns int) {
	up := len(v.names) - index
	v.emit(fmt.Sprintf("\x1b[%dA%s%s\x1b[%dB\r", up, clearLine, v.render(index, columns), up))
}
func (v *LegoView) set(index, state int, label string, now float64) {
	name := &v.names[index]
	if (name.State == 2 || name.State == 3) && state != 3 {
		return
	}
	if state == 1 && name.State != 1 {
		name.Started = now
	}
	changed := name.State != state || name.Label != label
	name.State = state
	name.Label = label
	if state == 1 {
		v.activity = name.Value + ": " + label
	}
	if !changed {
		return
	}
	_, columns := terminalSize(v.output)
	if v.list {
		v.redraw(index, columns)
	} else if state == 2 {
		v.emit(v.clear() + v.render(index, columns) + "\n")
	}
}
func (v *LegoView) errorLine(text string) {
	if text == "" || strings.HasPrefix(text, "error: one or more domains had a problem") {
		return
	}
	index := -1
	for strings.HasPrefix(text, "[") {
		end := strings.Index(text, "] ")
		if end < 0 {
			break
		}
		candidate, found := v.index[text[1:end]]
		if !found {
			break
		}
		index = candidate
		text = text[end+2:]
	}
	if index < 0 {
		if end := strings.Index(text, ": "); end >= 0 {
			if candidate, found := v.index[text[:end]]; found {
				index = candidate
				text = text[end+2:]
			}
		}
	}
	if index < 0 {
		return
	}
	v.names[index].Reason = shortLog(text)
	v.set(index, 3, shortLog(strings.TrimPrefix(text, "acme: ")), v.stamp)
}
func (v *LegoView) Feed(line LegoLine) {
	if line.Timestamp != "" {
		hour, _ := strconv.Atoi(line.Timestamp[11:13])
		minute, _ := strconv.Atoi(line.Timestamp[14:16])
		second, _ := strconv.Atoi(line.Timestamp[17:19])
		raw := float64(hour*3600 + minute*60 + second)
		if v.lastRaw >= 0 && raw+43200 < v.lastRaw {
			v.dayOffset += 86400
		}
		v.lastRaw = raw
		v.stamp = raw + v.dayOffset
		if v.firstStamp < 0 {
			v.firstStamp = v.stamp
		}
	}
	if line.fatalHeader() {
		v.inError = true
		v.activity = "certificate request failed"
		return
	}
	if line.domainFailure() || line.Timestamp == "" {
		if v.inError || line.domainFailure() {
			v.errorLine(line.Message)
		}
		return
	}
	message := line.Message
	switch {
	case strings.HasPrefix(message, "retry: "):
		v.activity = "retrying an ACME request"
		return
	case strings.Contains(message, "acme: Obtaining "):
		v.activity = "creating the order"
		return
	case strings.Contains(message, "acme: Validations succeeded"):
		v.activity = "requesting the certificate"
		return
	case strings.Contains(message, "Server responded with a certificate"):
		v.activity = "saving the certificate"
		return
	}
	if strings.HasPrefix(message, "[") {
		end := strings.Index(message, "] ")
		if end >= 0 {
			if index, found := v.index[message[1:end]]; found {
				v.domainEvent(index, message[end+2:])
				return
			}
		}
	}
	if strings.HasPrefix(message, "Wait for propagation") && v.current >= 0 {
		v.set(v.current, 1, "waiting for DNS propagation", v.stamp)
	} else if line.Level == "warning" || strings.HasPrefix(message, "cloudflare: failed") {
		v.addWarning(shortLog(message))
	}
}
func (v *LegoView) domainEvent(index int, message string) {
	switch {
	case strings.HasPrefix(message, "AuthURL: "):
		v.activity = "loading authorizations"
	case strings.HasPrefix(message, "acme: authorization already valid"):
		v.set(index, 2, "already verified", v.stamp)
	case strings.HasPrefix(message, "acme: Preparing to solve DNS-01"):
		v.current = index
		v.set(index, 1, "creating TXT record", v.stamp)
	case strings.HasPrefix(message, "acme: Trying to solve DNS-01"):
		v.current = index
		v.names[index].Started = v.stamp
		v.set(index, 1, "checking DNS", v.stamp)
	case strings.HasPrefix(message, "acme: Checking DNS record propagation"):
		v.set(index, 1, "checking DNS propagation", v.stamp)
	case strings.HasPrefix(message, "acme: Waiting for DNS record propagation"):
		v.set(index, 1, "waiting for DNS propagation", v.stamp)
	case strings.HasPrefix(message, "The server validated our request"):
		v.set(index, 2, "verified in "+duration(v.stamp-v.names[index].Started), v.stamp)
	case strings.HasPrefix(message, "acme: Cleaning DNS-01 challenge"):
		v.activity = "removing TXT records"
	case strings.HasPrefix(message, "acme: cleaning up failed: "):
		v.names[index].Cleanup = shortLog(strings.TrimPrefix(message, "acme: cleaning up failed: "))
	}
}
func (v *LegoView) addWarning(warning string) {
	for _, entry := range v.warnings {
		if entry == warning {
			return
		}
	}
	if len(v.warnings) < 32 {
		v.warnings = append(v.warnings, warning)
	}
}
func (v *LegoView) Finish(failure string) {
	v.Close()
	ok := v.verified()
	_, columns := terminalSize(v.output)
	for i := range v.names {
		name := &v.names[i]
		if name.Cleanup != "" && name.State == 2 {
			v.addWarning(name.Value + ": TXT record not removed: " + name.Cleanup)
		}
		if failure != "" {
			if name.State == 2 {
				v.set(i, 3, "DNS "+name.Label+", certificate failed", v.stamp)
			} else if name.Reason == "" {
				v.set(i, 3, "certificate failed", v.stamp)
			}
		}
	}
	if failure != "" && !v.list {
		for i := range v.names {
			v.emit(v.clear() + v.render(i, columns) + "\n")
		}
	}
	elapsed := time.Since(v.started).Seconds()
	if v.firstStamp >= 0 {
		elapsed = max(elapsed, v.stamp-v.firstStamp)
	}
	result := fmt.Sprintf("✅ Certificate received · %d/%d names verified · %s", ok, len(v.names), duration(elapsed))
	style := "1;32"
	if failure != "" {
		result = fmt.Sprintf("❌ No certificate | %d/%d DNS verified | %d failed | %s", ok, len(v.names), len(v.names), duration(elapsed))
		style = "1;31"
	}
	v.emit(v.clear() + fit(v.paint(result, style), columns-1) + "\n")
	if failure != "" {
		v.emit(v.paint("   "+failure, "31") + "\n")
	}
	details := 0
	for _, name := range v.names {
		if name.Reason != "" {
			if details < 5 {
				v.emit(v.paint("   "+name.Value+": "+name.Reason, "31") + "\n")
			}
			details++
		}
	}
	if details > 5 {
		v.emit(fmt.Sprintf("   %d more DNS errors in lego.log\n", details-5))
	}
	for i, warning := range v.warnings {
		if i == 5 {
			break
		}
		v.emit(v.paint("   ⚠️  "+warning, "33") + "\n")
	}
}
