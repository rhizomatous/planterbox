// Command plbxd is plbx's host-resident daemon.
//
// It owns sandbox state and lifecycle, and serves them over a unix socket. It
// is normally started on demand by plbx rather than run by hand; running it in
// a terminal is how you watch what it is doing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/rhizomatous/planterbox/internal/daemon"
	"github.com/rhizomatous/planterbox/internal/ui"
)

// version is set at build time, and must match the plbx that talks to it.
var version = "dev"

// main does nothing but choose a status, so run's deferred cleanup — releasing
// the signal handler, removing the socket and pidfile — actually happens.
func main() { os.Exit(run()) }

func run() int {
	var (
		socket      string
		stateDir    string
		proxyAddr   string
		relayImage  string
		showVersion bool
	)
	flag.StringVar(&socket, "socket", "", "unix socket to listen on (default: the runtime directory)")
	flag.StringVar(&stateDir, "state-dir", "", "where sandbox records are kept (default: XDG data dir)")
	flag.StringVar(&proxyAddr, "proxy", "", "address the egress proxy listens on (default: "+daemon.DefaultProxyAddr+")")
	flag.StringVar(&relayImage, "relay-image", "", "override the egress relay image")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("plbxd", version)
		return 0
	}

	// resolve the default here rather than logging an empty flag: where the
	// daemon listens is the one thing you need to know to reach it.
	if socket == "" {
		resolved, err := daemon.Socket(daemon.HostEnv(runtime.GOOS))
		if err != nil {
			ui.Log.Error(err.Error())
			return 1
		}
		socket = resolved
	}

	// SIGTERM is how `plbx daemon stop` asks; SIGINT is how a terminal does.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	opts := daemon.Options{
		Socket:     socket,
		StateDir:   stateDir,
		ProxyAddr:  proxyAddr,
		RelayImage: relayImage,
		Version:    version,
	}
	opts.Ready = func() {
		ui.Log.Info("plbxd listening", "socket", socket, "proxy", daemon.ProxyAddress(opts))
	}

	err := daemon.Serve(ctx, opts)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, daemon.ErrAlreadyRunning):
		// another daemon is serving, which is what the caller wanted anyway.
		ui.Log.Warn(err.Error())
		return 0
	default:
		ui.Log.Error(err.Error())
		return 1
	}
}
