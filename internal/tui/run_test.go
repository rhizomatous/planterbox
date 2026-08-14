package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestRunStartsAndStopsWithTheContext drives the whole program loop — Init,
// Update, View — the way bubbletea does, rather than calling the model's
// methods directly. It is what catches a panic in Init or View that the model
// tests, which never construct a tea.View, would miss.
//
// It ends the program by cancelling the context rather than by sending a key.
// Feeding keys through a plain reader races the program's startup, and a test
// that sometimes waits for a deadline is worse than no test at all. Which keys
// do what is covered exhaustively in model_test.go.
func TestRunStartsAndStopsWithTheContext(t *testing.T) {
	fake := api.NewFake(sandbox("alpha", api.StatusRunning), sandbox("beta", api.StatusStopped))
	var out bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// an empty reader keeps the program running until the context ends it.
		_, _ = Run(ctx, fake, Options{Input: strings.NewReader(""), Output: &out})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
