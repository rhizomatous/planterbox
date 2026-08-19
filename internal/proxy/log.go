package proxy

import (
	"sync"
	"time"
)

// defaultLogLimit is how many decisions the log keeps. Enough to cover what a
// dashboard shows and what a person scrolling back would want, without the
// daemon's memory growing with the agent's chattiness.
const defaultLogLimit = 500

// Entry is one decision the proxy made.
type Entry struct {
	// Seq orders entries and identifies them. It never repeats, so a reader
	// that remembers the last one it saw can ask only for what is new.
	Seq     uint64    `json:"seq"`
	At      time.Time `json:"at"`
	Target  Target    `json:"target"`
	Allowed bool      `json:"allowed"`
	Reason  string    `json:"reason"`
	// Sandbox names who asked, when the request carried a token saying so.
	Sandbox string `json:"sandbox,omitempty"`
}

// Log is a bounded record of recent decisions, safe for concurrent use.
//
// It is deliberately not a stream. The dashboard already re-reads on a tick,
// and a bounded log read by sequence gives it what changed without the daemon
// holding a subscription per viewer.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	limit   int
	seq     uint64
}

// NewLog returns a log keeping at most limit entries. A limit below one uses
// the default.
func NewLog(limit int) *Log {
	if limit < 1 {
		limit = defaultLogLimit
	}
	return &Log{limit: limit}
}

// Record adds a decision and returns it as stored, sequence and all.
func (l *Log) Record(e Entry) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	e.Seq = l.seq
	if e.At.IsZero() {
		e.At = time.Now()
	}

	l.entries = append(l.entries, e)
	if excess := len(l.entries) - l.limit; excess > 0 {
		// keep the tail, and let the dropped head be collected rather than
		// pinning it behind a slice that still references the old array.
		l.entries = append(l.entries[:0], l.entries[excess:]...)
	}
	return e
}

// Since returns the entries recorded after seq, oldest first. Passing zero
// returns everything the log still holds.
func (l *Log) Since(seq uint64) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// Recent returns the last n entries, oldest first.
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	if n <= 0 || n > len(l.entries) {
		n = len(l.entries)
	}
	out := make([]Entry, n)
	copy(out, l.entries[len(l.entries)-n:])
	return out
}
