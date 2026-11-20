package certgen

import (
	"fmt"
	"io"
	"os"
)

// RunCLI handles expected errors and converts unexpected panics into a concise,
// redacted failure. Process supervision cleans up children before this recovery.
func RunCLI(arguments []string, input, output, errorOutput *os.File) int {
	redactor := NewRedactor()
	return recoverStatus(redactor, errorOutput, func() int {
		signals := WatchSignals()
		defer signals.Close()
		app := App{Input: input, Output: output, Error: errorOutput, Signals: signals, Redactor: redactor}
		return app.Run(arguments)
	})
}

func recoverStatus(redactor *Redactor, output io.Writer, operation func() int) (status int) {
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintln(output, "Internal error:", redactor.Safe(fmt.Sprint(failure)))
			status = 2
		}
	}()
	return operation()
}
