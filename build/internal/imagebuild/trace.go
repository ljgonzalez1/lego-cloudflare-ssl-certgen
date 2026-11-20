package imagebuild

// Progress is installed by the single-command build CLI. Library callers and
// offline fixtures remain quiet unless they explicitly install a reporter.
var Progress func(string, ...any)

func Tracef(format string, args ...any) {
	if Progress != nil {
		Progress(format, args...)
	}
}
