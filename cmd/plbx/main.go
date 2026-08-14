// plbx manages persistent, isolated container sandboxes for coding agents.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

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

	err := fang.Execute(ctx, newRootCmd(), fang.WithVersion(buildVersion()))
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
