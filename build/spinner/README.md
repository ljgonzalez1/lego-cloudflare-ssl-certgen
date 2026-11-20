# Terminal pulse

Independent Go package using only the standard library. It belongs to the
project's existing Go module and can be imported without internal-package
restrictions. It does not start a goroutine, own a timer, write output or change
terminal settings. The caller controls drawing and cleanup.

```go
import (
    "time"

    "github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/spinner"
)

// Call from an existing terminal rendering loop.
frame := spinner.At(time.Since(started))
text := frame.Render(true) // false returns the three glyphs without ANSI
```

`Period` is 1.4 seconds. `Interval` is 28 milliseconds, about 36 redraws/second.
The seven Unicode states begin at 0%, 10%, 18%, 26%, 34%, 42% and 50%, matching
the supplied `sp-pls` CSS. The initial state rests through the second half of
the cycle. Frames are sampled from elapsed time, so delayed redraws never slow
the animation or change its period.

`Frame.Level` follows cubic-bezier(0.1, 0.9, 0.2, 1), rises to its peak at 18%
and returns to rest at 50%. `Render(true)` maps it to four standard ANSI cyan
intensity styles, with a reset after every frame. Actual dim/bold appearance
depends on the terminal theme. Browser opacity, blur, drop shadows, filters and
gradient backgrounds have no direct terminal equivalent.

The application draws this pulse beside each domain currently being checked,
as well as on the overall status line in the interactive pretty progress view.
Pending domains retain their normal icon. Verified and failed domains stop
animating immediately. Domain rows are redrawn only when the glyph or ANSI
intensity changes; the overall status line uses the existing timer loop.
When the terminal is too short for a domain list, the compact status line shows
the pulse alongside the current domain and activity when they fit the width.
Redirected output, raw lego output, `TERM=dumb`, unknown or empty
TERM, and the existing nonempty `NO_COLOR` setting do not animate. The pulse
occupies three columns on terminals with single-column Braille glyph support.
