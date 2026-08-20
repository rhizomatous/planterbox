// Command plbx-relay carries traffic across a sandbox's network boundary.
//
// It exists because of what an internal network does. A sandbox is alone on
// one, which leaves it with no route out, no route in, and no way to reach the
// host. Anything that has to cross that line runs here, in a container
// attached to both sides.
//
// It serves both directions. Egress: one forward from the sandbox network to
// the proxy on the host, which is where policy and the connection log live.
// Ingress: one forward per published port, from a host-published port to the
// sandbox, because a runtime silently drops --publish on an internal network.
//
// It is deliberately incurious. It reads no bytes, makes no decisions, and can
// reach only the addresses it was started with. Everything that decides
// anything is on the far side of it. See docs/concessions.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

var version = "dev"

// forward is one listening address and the single place it carries traffic to.
type forward struct {
	listen   string
	upstream string
}

// forwards collects the repeatable -forward flag.
type forwards []forward

func (f *forwards) String() string {
	parts := make([]string, 0, len(*f))
	for _, fwd := range *f {
		parts = append(parts, fwd.listen+"="+fwd.upstream)
	}
	return strings.Join(parts, ",")
}

// Set reads "LISTEN=UPSTREAM". The listen side is split on the last "=" so an
// upstream is free to contain one, and both halves must be present: a forward
// missing either end would listen on everything or dial nothing.
func (f *forwards) Set(s string) error {
	listen, upstream, ok := strings.Cut(s, "=")
	if !ok || listen == "" || upstream == "" {
		return fmt.Errorf("expected LISTEN=UPSTREAM, got %q", s)
	}
	*f = append(*f, forward{listen: listen, upstream: upstream})
	return nil
}

func main() { os.Exit(run()) }

func run() int {
	var (
		listen      string
		upstream    string
		pairs       forwards
		showVersion bool
	)
	flag.StringVar(&listen, "listen", ":8080", "address to accept connections on, with -upstream")
	flag.StringVar(&upstream, "upstream", "", "the single address to forward to, as host:port")
	flag.Var(&pairs, "forward", "an additional LISTEN=UPSTREAM forward (repeatable)")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("plbx-relay", version)
		return 0
	}
	if upstream != "" {
		pairs = append(pairs, forward{listen: listen, upstream: upstream})
	}
	if len(pairs) == 0 {
		log.Println("plbx-relay: nothing to forward: pass -upstream or -forward")
		return 2
	}

	listeners := make([]net.Listener, 0, len(pairs))
	for _, fwd := range pairs {
		lis, err := net.Listen("tcp", fwd.listen)
		if err != nil {
			log.Printf("plbx-relay: listening on %s: %v", fwd.listen, err)
			closeAll(listeners)
			return 1
		}
		listeners = append(listeners, lis)
		log.Printf("plbx-relay: %s -> %s", fwd.listen, fwd.upstream)
	}

	// closing the listeners is what ends Accept, so the signal handler does
	// that rather than exiting out from under connections in flight.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		closeAll(listeners)
	}()

	var wg sync.WaitGroup
	for i, lis := range listeners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			accept(lis, pairs[i].upstream)
		}()
	}
	wg.Wait()
	return 0
}

// accept serves one listener until it closes.
func accept(lis net.Listener, upstream string) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return // the listener closed, which is how this stops
		}
		go forwardConn(conn, upstream)
	}
}

func closeAll(listeners []net.Listener) {
	for _, lis := range listeners {
		_ = lis.Close()
	}
}

// forwardConn joins one accepted connection to a fresh upstream one.
//
// The upstream is dialled per connection rather than held open, so a forward
// outlives whatever it points at: a sandbox that is not running yet refuses
// this connection and serves the next one once it is.
func forwardConn(client net.Conn, upstream string) {
	defer func() { _ = client.Close() }()

	server, err := net.Dial("tcp", upstream)
	if err != nil {
		log.Printf("plbx-relay: dialling %s: %v", upstream, err)
		return
	}
	defer func() { _ = server.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyThrough(server, client) }()
	go func() { defer wg.Done(); copyThrough(client, server) }()
	wg.Wait()
}

// copyThrough moves bytes one way and then half-closes, so the far side sees
// the end of the stream instead of waiting on it.
func copyThrough(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
