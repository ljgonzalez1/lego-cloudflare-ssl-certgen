package spinner_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/spinner"
)

func TestUnicodeTransitionsAndCycle(t *testing.T) {
	for _, item := range []struct {
		at   time.Duration
		text string
	}{
		{-time.Second, "⠀⠶⠀"},
		{0, "⠀⠶⠀"},
		{140*time.Millisecond - 1, "⠀⠶⠀"},
		{140 * time.Millisecond, "⠰⣿⠆"},
		{252*time.Millisecond - 1, "⠰⣿⠆"},
		{252 * time.Millisecond, "⢾⣿⡷"},
		{364 * time.Millisecond, "⢾⣉⡷"},
		{476 * time.Millisecond, "⣏⣉⣹"},
		{588 * time.Millisecond, "⡁⠀⢈"},
		{700 * time.Millisecond, "⠀⠶⠀"},
		{1399 * time.Millisecond, "⠀⠶⠀"},
		{spinner.Period, "⠀⠶⠀"},
		{9*spinner.Period + 252*time.Millisecond, "⢾⣿⡷"},
	} {
		t.Run(item.at.String(), func(t *testing.T) {
			frame := spinner.At(item.at)
			if frame.Text != item.text {
				t.Fatalf("got %q, want %q", frame.Text, item.text)
			}
			if utf8.RuneCountInString(frame.Text) != 3 {
				t.Fatal("pulse changed its three-column width")
			}
		})
	}
}

func TestBloomRisesFallsAndRests(t *testing.T) {
	peak := 252 * time.Millisecond
	if spinner.At(0).Level != 0 || spinner.At(peak).Level != 1 {
		t.Fatal("bloom endpoints do not match the CSS")
	}
	previous := 0.0
	for elapsed := time.Duration(0); elapsed <= peak; elapsed += time.Millisecond {
		level := spinner.At(elapsed).Level
		if level < previous || level < 0 || level > 1 {
			t.Fatalf("invalid rising level %g at %s", level, elapsed)
		}
		previous = level
	}
	previous = 1
	for elapsed := peak; elapsed <= spinner.Period; elapsed += time.Millisecond {
		level := spinner.At(elapsed).Level
		if level > previous || level < 0 || level > 1 {
			t.Fatalf("invalid falling level %g at %s", level, elapsed)
		}
		if elapsed >= spinner.Period/2 && level != 0 {
			t.Fatal("second half of the cycle must rest")
		}
		previous = level
	}
	// At half the rise, the supplied curve is already close to its peak.
	if spinner.At(peak/2).Level < 0.9 {
		t.Fatal("bloom easing was replaced with a linear ramp")
	}
}

func TestRenderBoundsTerminalStyling(t *testing.T) {
	for elapsed := time.Duration(0); elapsed < spinner.Period; elapsed += spinner.Interval {
		frame := spinner.At(elapsed)
		if frame.Render(false) != frame.Text || strings.Contains(frame.Render(false), "\x1b") {
			t.Fatal("plain rendering emitted terminal controls")
		}
		if rendered := frame.Render(true); !strings.HasSuffix(rendered, "\x1b[0m") || !strings.Contains(rendered, frame.Text) {
			t.Fatalf("styling leaked: %q", rendered)
		}
	}
	if spinner.At(0).Render(true) == spinner.At(252*time.Millisecond).Render(true) {
		t.Fatal("rest and bloom must have different styles")
	}
}
