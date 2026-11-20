// Package spinner renders a stateless terminal pulse based on elapsed time.
// It owns no timer, goroutine, terminal state or output stream.
package spinner

import "time"

// Period matches the supplied CSS animation's 1.4-second cycle.
const Period = 1400 * time.Millisecond

// Interval lands on every Unicode transition and yields about 36 frames/second.
const Interval = 28 * time.Millisecond

// Frame contains three single-column Braille glyphs and normalized brightness.
// Level is zero at rest and one at the bloom's peak.
type Frame struct {
	Text  string
	Level float64
}

// At samples the pulse without advancing mutable state. Negative times show
// the initial frame; slow or irregular callers stay synchronized with time.
func At(elapsed time.Duration) Frame {
	if elapsed < 0 {
		elapsed = 0
	}
	phase := elapsed % Period
	text := "⠀⠶⠀"
	switch {
	case phase >= Period*50/100:
	case phase >= Period*42/100:
		text = "⡁⠀⢈"
	case phase >= Period*34/100:
		text = "⣏⣉⣹"
	case phase >= Period*26/100:
		text = "⢾⣉⡷"
	case phase >= Period*18/100:
		text = "⢾⣿⡷"
	case phase >= Period*10/100:
		text = "⠰⣿⠆"
	}
	peak, rest := Period*18/100, Period/2
	level := 0.0
	if phase <= peak {
		level = bloomEase(float64(phase) / float64(peak))
	} else if phase < rest {
		level = 1 - bloomEase(float64(phase-peak)/float64(rest-peak))
	}
	return Frame{Text: text, Level: level}
}

// Render returns bare glyphs when ansi is false. Otherwise standard SGR cyan
// intensity approximates CSS opacity/brightness; terminals cannot draw blur,
// drop-shadow, background gradients or browser brightness filters.
// The reset is confined to the glyphs so the caller can style adjacent text.
func (f Frame) Render(ansi bool) string {
	if !ansi {
		return f.Text
	}
	style := "2;36"
	switch {
	case f.Level >= 0.85:
		style = "1;96"
	case f.Level >= 0.5:
		style = "1;36"
	case f.Level >= 0.2:
		style = "36"
	}
	return "\x1b[" + style + "m" + f.Text + "\x1b[0m"
}

// bloomEase evaluates cubic-bezier(0.1, 0.9, 0.2, 1). The curve's x coordinate
// is monotonic; bounded bisection inverts it without a dependency or allocation.
func bloomEase(progress float64) float64 {
	if progress <= 0 {
		return 0
	}
	if progress >= 1 {
		return 1
	}
	low, high := 0.0, 1.0
	for i := 0; i < 24; i++ {
		t := (low + high) / 2
		u := 1 - t
		x := 3*u*u*t*0.1 + 3*u*t*t*0.2 + t*t*t
		if x < progress {
			low = t
		} else {
			high = t
		}
	}
	t := (low + high) / 2
	u := 1 - t
	return 3*u*u*t*0.9 + 3*u*t*t + t*t*t
}
