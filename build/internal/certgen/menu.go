package certgen

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const keyPrevious = 1000
const keyNext = 1001
const keyEnter = 1002
const keyBack = 1003
const keyCancel = 1004
const keyNone = 1005

type Menu struct {
	Input, Output *os.File
	Context       context.Context
	ANSI          bool
	interrupted   bool
}

func supportsANSI() bool {
	term := os.Getenv("TERM")
	return term != "" && term != "dumb" && term != "unknown" && os.Getenv("NO_COLOR") == ""
}
func (m *Menu) readByte(timeout time.Duration) (int, error) {
	started := time.Now()
	for {
		select {
		case <-m.Context.Done():
			m.interrupted = true
			return keyCancel, nil
		default:
		}
		wait := 200 * time.Millisecond
		if timeout >= 0 {
			wait = timeout - time.Since(started)
			if wait <= 0 {
				return keyNone, nil
			}
		}
		ready, err := readyInput(m.Input, wait)
		if err != nil {
			return keyCancel, err
		}
		if !ready {
			if timeout >= 0 {
				return keyNone, nil
			}
			continue
		}
		var value [1]byte
		n, err := m.Input.Read(value[:])
		if n == 0 {
			return keyCancel, err
		}
		if value[0] == 3 {
			m.interrupted = true
			return keyCancel, nil
		}
		return int(value[0]), nil
	}
}
func (m *Menu) readKey() (int, error) {
	key, err := m.readByte(-1)
	if err != nil {
		return keyCancel, err
	}
	switch key {
	case keyCancel, 4:
		return keyCancel, nil
	case '\r', '\n':
		return keyEnter, nil
	case 'h', 'k':
		return keyPrevious, nil
	case '\t', 'j', 'l':
		return keyNext, nil
	case 27:
		prefix, err := m.readByte(120 * time.Millisecond)
		if err != nil {
			return keyCancel, err
		}
		if prefix == keyNone || prefix == 27 {
			return keyBack, nil
		}
		if prefix == keyCancel {
			return keyCancel, nil
		}
		if prefix != '[' && prefix != 'O' {
			return keyNone, nil
		}
		var parameters strings.Builder
		tooLong := false
		code, err := m.readByte(120 * time.Millisecond)
		for err == nil && code >= 32 && code < 64 {
			if parameters.Len() < 16 {
				parameters.WriteByte(byte(code))
			} else {
				tooLong = true
			}
			code, err = m.readByte(120 * time.Millisecond)
		}
		if err != nil {
			return keyCancel, err
		}
		if tooLong {
			return keyNone, nil
		}
		if prefix == '[' && parameters.String() == "200" && code == '~' {
			end := "\x1b[201~"
			matched := 0
			for {
				ch, err := m.readByte(-1)
				if err != nil || ch == keyCancel {
					return keyCancel, err
				}
				if byte(ch) == end[matched] {
					matched++
				} else if byte(ch) == end[0] {
					matched = 1
				} else {
					matched = 0
				}
				if matched == len(end) {
					return keyNone, nil
				}
			}
		}
		switch code {
		case 'A', 'D', 'Z':
			return keyPrevious, nil
		case 'B', 'C':
			return keyNext, nil
		case keyCancel:
			return keyCancel, nil
		}
		return keyNone, nil
	}
	return keyNone, nil
}
func choiceText(label string, selected bool) string {
	if selected {
		return " > " + label + " < "
	}
	return "   " + label + "   "
}
func (m *Menu) draw(title string, details, options []string, selected int, alert bool) error {
	_, columns := terminalSize(m.Output)
	boxed := columns >= 22
	width := columns - 1
	if width > 76 {
		width = 76
	}
	if boxed {
		width -= 4
	}
	if width < 1 {
		width = 1
	}
	var page strings.Builder
	accent, reset := "", ""
	if m.ANSI {
		accent = "\x1b[36m"
		if alert {
			accent = "\x1b[33m"
		}
		reset = "\x1b[0m"
		page.WriteString("\x1b[H\x1b[2J")
	} else {
		page.WriteByte('\n')
	}
	if m.ANSI && columns >= 30 {
		if alert {
			page.WriteString("⚠️  Let's Encrypt production\n\n")
		} else {
			page.WriteString("🔐 Certificate issuance\n\n")
		}
	}
	rule := func() {
		if boxed {
			fmt.Fprintf(&page, "%s+%s+%s\n", accent, strings.Repeat("-", width+2), reset)
		}
	}
	row := func(content string, length int) {
		if boxed {
			page.WriteString(accent + "| " + reset)
		}
		page.WriteString(content)
		if boxed {
			page.WriteString(strings.Repeat(" ", max(0, width-length)) + accent + " |" + reset)
		}
		page.WriteByte('\n')
	}
	wrap := func(text string, bold bool) {
		for {
			count := min(width, len(text))
			if count < len(text) {
				if at := strings.LastIndexByte(text[:count], ' '); at > 0 {
					count = at
				}
			}
			part := text[:count]
			if m.ANSI && bold {
				part = "\x1b[1m" + part + reset
			}
			row(part, count)
			text = strings.TrimPrefix(text[count:], " ")
			if text == "" {
				break
			}
		}
	}
	rule()
	wrap(title, true)
	rule()
	for _, line := range details {
		wrap(line, false)
	}
	row("", 0)
	var buttons strings.Builder
	used := 0
	for i, label := range options {
		text := choiceText(label, i == selected)
		if used > 0 && used+len(text) > width {
			row(buttons.String(), used)
			buttons.Reset()
			used = 0
		}
		if m.ANSI && i == selected {
			buttons.WriteString("\x1b[1;7m")
		}
		buttons.WriteString(text)
		if m.ANSI && i == selected {
			buttons.WriteString(reset)
		}
		used += len(text)
	}
	if used <= width {
		row(buttons.String(), used)
	} else {
		wrap(buttons.String(), false)
	}
	rule()
	wrap("Arrows or Tab: move | Enter: select | Esc: back | Ctrl+C: exit", false)
	_, err := fmt.Fprint(m.Output, page.String())
	return err
}
func (m *Menu) choose(title string, details, options []string, selected int, alert bool) (int, error) {
	if err := flushTerminal(m.Input); err != nil {
		return keyCancel, err
	}
	for {
		if err := m.draw(title, details, options, selected, alert); err != nil {
			return keyCancel, err
		}
		for {
			key, err := m.readKey()
			if err != nil {
				return keyCancel, err
			}
			switch key {
			case keyPrevious:
				selected = (selected + len(options) - 1) % len(options)
			case keyNext:
				selected = (selected + 1) % len(options)
			case keyEnter:
				return selected, nil
			case keyBack:
				return keyBack, nil
			case keyCancel:
				return keyCancel, nil
			default:
				continue
			}
			break
		}
	}
}
func (m *Menu) Run() (mode string, err error) {
	if !isTerminal(m.Input) || !isTerminal(m.Output) {
		return "", fmt.Errorf("The issuance menu needs a terminal. Use docker run -it, --test for offline checks, or an explicit mode for unattended issuance.")
	}
	original, err := terminalAttributes(m.Input)
	if err != nil {
		return "", err
	}
	raw := original
	raw.Lflag &^= syscallMenuLocalFlags()
	raw.Iflag &^= syscallMenuInputFlags()
	setMenuControlBytes(&raw)
	if err = setTerminal(m.Input, &raw); err != nil {
		return "", err
	}
	if m.ANSI {
		fmt.Fprint(m.Output, "\x1b[?1049h\x1b[?25l\x1b[?2004h")
	}
	defer func() {
		restore := setTerminal(m.Input, &original)
		if m.ANSI {
			fmt.Fprint(m.Output, "\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
		}
		if err == nil && restore != nil {
			err = restore
		}
	}()
	for {
		selected, err := m.choose("Choose an issuance environment", []string{"Production issues browser-trusted certificates and has rate limits.", "Staging tests DNS and issuance with untrusted certificates."}, []string{"Staging", "Production", "Cancel"}, 0, false)
		if err != nil {
			return "", err
		}
		if selected == keyCancel || selected == keyBack || selected == 2 {
			return "cancel", nil
		}
		title := "Confirm staging issuance"
		details := []string{"This contacts Let's Encrypt staging and updates DNS challenge records.", "Browsers do not trust the resulting certificates."}
		mode = "staging"
		if selected == 1 {
			title = "Confirm production issuance"
			details = []string{"This submits live orders for browser-trusted certificates.", "Production has rate limits per registered domain and exact set of names.", "Repeated requests can require waiting before another certificate is allowed.", "https://letsencrypt.org/docs/rate-limits/"}
			mode = "production"
		}
		accepted, err := m.choose(title, details, []string{"Yes", "No"}, 1, selected == 1)
		if err != nil {
			return "", err
		}
		if accepted == keyCancel {
			return "cancel", nil
		}
		if accepted == 0 {
			return mode, nil
		}
	}
}
