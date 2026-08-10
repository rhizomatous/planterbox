package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"

	"github.com/spf13/cobra"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/daemon"
)

func newSSHProxyCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-proxy HOST",
		Short: "carry an ssh connection to a sandbox",
		Long: "Connect ssh to a sandbox. This is what `jard setup ssh` puts in your " +
			"ProxyCommand, and it is not useful to run by hand.\n\n" +
			"It writes the sandbox's name to jard's ssh socket and then relays bytes. " +
			"There is no port anywhere in this: the socket is readable only by you, " +
			"which is what stands in for network access control.",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sshProxy(cmd, g, args[0])
		},
	}
}

// sshProxy joins this process's stdio to the daemon's ssh socket.
func sshProxy(cmd *cobra.Command, g *globals, host string) error {
	// the daemon holds the gateway, so it has to be up. Connecting is what
	// starts one, and closing it again leaves it running.
	svc, err := g.open(cmd)
	if err != nil {
		return err
	}
	_ = svc.Close()

	socket, err := daemon.SSHSocket(daemon.HostEnv(runtime.GOOS))
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("connecting to jard's ssh gateway: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// which sandbox, before any ssh bytes. See internal/sshd/route.go.
	if _, err := io.WriteString(conn, host+"\n"); err != nil {
		return fmt.Errorf("naming the sandbox: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, os.Stdin)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, conn)
	}()
	wg.Wait()
	return nil
}

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "wire jard into the tools around it",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSetupSSHCmd())
	return cmd
}

func newSetupSSHCmd() *cobra.Command {
	var (
		path     string
		toStdout bool
	)

	cmd := &cobra.Command{
		Use:   "ssh",
		Short: "let ssh and editors reach sandboxes as <name>." + api.SSHDomain,
		Long: "Add a managed block to your ssh config, so that `ssh myrepo." + api.SSHDomain + "` " +
			"opens a shell in the sandbox named myrepo, and editors that speak ssh can " +
			"attach to one.\n\n" +
			"Nothing listens on a port. ssh reaches the sandbox through a ProxyCommand " +
			"onto jard's own socket, which only you can open.\n\n" +
			"The block is rewritten in place on every run, and everything outside its " +
			"markers is left alone. --print writes it to stdout instead, if you would " +
			"rather paste it yourself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("finding this binary, which ssh has to call back into: %w", err)
			}
			known, err := knownHostsPath()
			if err != nil {
				return err
			}
			block := sshConfigBlock(exe, known)

			if toStdout {
				_, err := fmt.Fprint(cmd.OutOrStdout(), block)
				return err
			}
			if path == "" {
				if path, err = sshConfigPath(); err != nil {
					return err
				}
			}
			written, err := writeManagedBlock(path, block)
			if err != nil {
				return err
			}
			return reportSetup(cmd, path, written)
		},
	}

	cmd.Flags().StringVar(&path, "config", "", "ssh config to edit (default: ~/.ssh/config)")
	cmd.Flags().BoolVar(&toStdout, "print", false, "write the block to stdout instead of editing anything")
	return cmd
}
