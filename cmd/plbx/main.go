// plbx manages persistent, isolated container sandboxes for coding agents.
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/fang"
)

// version is exactly what it says on the tin.
var version = "dev"

func main() {
	os.Exit(run())
}

// run returns an exit code rather than calling os.Exit, which would skip the
// deferred stop.
func run() int {
	// Ctrl-C should unwind cleanly rather than orphan a container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := fang.Execute(ctx, newRootCmd(),
		fang.WithVersion(buildVersion()),
		fang.WithErrorHandler(renderError),
	)
	if err == nil {
		return 0
	}
	// an agent's own exit status passes straight through, so `plbx run` is as
	// usable in a script as running the agent directly would be.
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	// fang prints anything else itself, styled.
	return 1
}

// renderError prints an error the way fang does, with its casing transform
// replaced.
//
// fang sentence-cases an error by running the first whitespace-delimited word
// through Unicode title casing, which treats a hyphen as a word boundary. Our
// errors lead with a sandbox name and our sandbox names are hyphenated by
// construction, so `foo-bar: sandbox not found` renders as `Foo-Bar`:
// a name that does not exist, shown to someone who is at that moment checking
// whether they typed one correctly.
func renderError(w io.Writer, styles fang.Styles, err error) {
	styles.ErrorText = styles.ErrorText.Transform(sentenceCase)
	fang.DefaultErrorHandler(w, styles, err)
}

// sentenceCase upper-cases the first rune and leaves every other one alone.
func sentenceCase(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// buildVersion resolves the string shown by --version. It prefers a value
// injected via -ldflags, then falls back to VCS build info if absent.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	// set when installed via `go install ...@version`
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	// otherwise synthesize from the embedded git revision
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev := s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return "dev-" + rev
		}
	}
	return version
}
