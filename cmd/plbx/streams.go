package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/rhizomatous/planterbox/internal/api"
)

// hostStreams wires a session to this process's own stdio.
//
// The returned stop must be called when the session ends: it releases the
// SIGWINCH subscription and closes the resize channel, which is what lets the
// far side stop tracking a terminal that is no longer there.
func hostStreams(tty bool) (streams api.Streams, stop func()) {
	streams = api.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if !tty {
		return streams, func() {}
	}

	sizes := make(chan api.Size, 1)
	// the opening size goes in before this returns, rather than from the
	// goroutine below. Produced asynchronously it would race the session's
	// start, and a session that starts without one gets a 0x0 terminal.
	if size, ok := terminalSize(); ok {
		sizes <- size
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	done := make(chan struct{})
	go func() {
		defer close(sizes)
		for {
			select {
			case <-winch:
			case <-done:
				return
			}
			if size, ok := terminalSize(); ok {
				select {
				case sizes <- size:
				case <-done:
					return
				}
			}
		}
	}()

	streams.Resize = sizes
	return streams, func() {
		signal.Stop(winch)
		close(done)
	}
}

// terminalSize reads the controlling terminal's dimensions, reporting false
// when there is no terminal to read.
func terminalSize() (api.Size, bool) {
	width, height, err := term.GetSize(os.Stdin.Fd())
	if err != nil {
		return api.Size{}, false
	}
	return api.Size{Rows: uint16(height), Cols: uint16(width)}, true
}
