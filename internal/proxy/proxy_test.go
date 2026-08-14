package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These run a real proxy over a real socket against a real upstream. The
// address guard and name resolution are redirected at the loopback stub, since
// the guard would otherwise refuse it on sight — which is the guard working.

// upstream starts a stub origin server and returns its host:port.
func upstream(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// proxyFor starts a proxy that resolves every name to origin, and returns the
// URL a client should use to reach it.
func proxyFor(t *testing.T, policy Policy, origin string) (*url.URL, *Server) {
	t.Helper()

	host, port, err := net.SplitHostPort(origin)
	if err != nil {
		t.Fatalf("splitting %q: %v", origin, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		t.Fatalf("parsing %q: %v", host, err)
	}

	srv := NewServer(
		func() Policy { return policy },
		WithLookup(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{addr}, nil
		}),
		WithDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			// ignore the requested port: everything goes to the stub.
			d := net.Dialer{}
			return d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		}),
		WithAddressGuard(func(netip.Addr) bool { return true }),
	)

	listener := httptest.NewServer(srv)
	t.Cleanup(listener.Close)

	u, err := url.Parse(listener.URL)
	if err != nil {
		t.Fatalf("parsing the proxy url: %v", err)
	}
	return u, srv
}

// through returns a client that sends everything via the proxy.
func through(u *url.URL) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(u),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a stub origin with a self-signed cert
		},
	}
}

func TestAllowedRequestReachesTheOrigin(t *testing.T) {
	origin := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from upstream")
	})
	p := Policy{Preset: PresetBalanced, Rules: []Rule{{Pattern: "allowed.test", Allow: true}}}
	u, _ := proxyFor(t, p, origin)

	resp, err := through(u).Get("http://allowed.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q, want the origin's response", body)
	}
}

func TestDeniedRequestIsRefusedWithAReason(t *testing.T) {
	origin := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the origin should never have been reached")
	})
	u, _ := proxyFor(t, New(PresetBalanced), origin)

	resp, err := through(u).Get("http://blocked.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// the message reaches whoever is reading the agent's output, so it has to
	// name the host and say what to do about it.
	if !strings.Contains(string(body), "blocked.test") {
		t.Errorf("body = %q, want it to name the host", body)
	}
	if !strings.Contains(string(body), "balanced") {
		t.Errorf("body = %q, want it to say what denied the request", body)
	}
}

func TestConnectTunnelsAnAllowedHost(t *testing.T) {
	// end to end over TLS the proxy never reads: the point of a tunnel.
	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret payload")
	}))
	defer tlsOrigin.Close()

	p := Policy{Preset: PresetBalanced, Rules: []Rule{{Pattern: "allowed.test", Allow: true}}}
	u, _ := proxyFor(t, p, strings.TrimPrefix(tlsOrigin.URL, "https://"))

	resp, err := through(u).Get("https://allowed.test/")
	if err != nil {
		t.Fatalf("Get over CONNECT: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secret payload" {
		t.Errorf("body = %q, want the tunnelled response", body)
	}
}

func TestConnectRefusesADeniedHost(t *testing.T) {
	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the origin should never have been reached")
	}))
	defer tlsOrigin.Close()

	u, _ := proxyFor(t, New(PresetBalanced), strings.TrimPrefix(tlsOrigin.URL, "https://"))

	resp, err := through(u).Get("https://blocked.test/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a denied CONNECT should not produce a usable connection")
	}
}

func TestPolicyIsReadPerRequest(t *testing.T) {
	// `plbx policy allow` has to take effect on the next connection, not the
	// next daemon.
	origin := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	policy := New(PresetBalanced)
	host, port, _ := net.SplitHostPort(origin)
	addr, _ := netip.ParseAddr(host)
	srv := NewServer(
		func() Policy { return policy },
		WithLookup(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{addr}, nil
		}),
		WithDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		}),
		WithAddressGuard(func(netip.Addr) bool { return true }),
	)
	listener := httptest.NewServer(srv)
	defer listener.Close()
	u, _ := url.Parse(listener.URL)

	resp, err := through(u).Get("http://later.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want it denied to start with", resp.StatusCode)
	}

	policy.Rules = append(policy.Rules, Rule{Pattern: "later.test", Allow: true})

	resp, err = through(u).Get("http://later.test/")
	if err != nil {
		t.Fatalf("Get after allowing: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want the new rule to apply immediately", resp.StatusCode)
	}
}

func TestANameResolvingIntoPrivateSpaceIsRefused(t *testing.T) {
	// the policy names hosts and the guard covers addresses. Without the
	// second check, "evil.test A 127.0.0.1" would pass a hostname allow and
	// land on the host's own network.
	origin := upstream(t, func(http.ResponseWriter, *http.Request) {
		t.Error("a name resolving into private space must never be dialled")
	})
	host, port, _ := net.SplitHostPort(origin)

	srv := NewServer(
		func() Policy { return Policy{Preset: PresetOpen} },
		WithLookup(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}),
		WithDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		}),
		// the real guard, deliberately.
	)
	listener := httptest.NewServer(srv)
	defer listener.Close()
	u, _ := url.Parse(listener.URL)

	resp, err := through(u).Get("http://rebind.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Error("a name resolving to loopback was allowed through")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "may not reach") {
		t.Errorf("body = %q, want it to say the address was refused", body)
	}
}

func TestDecisionsAreLogged(t *testing.T) {
	origin := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	p := Policy{Preset: PresetBalanced, Rules: []Rule{{Pattern: "allowed.test", Allow: true}}}
	u, srv := proxyFor(t, p, origin)

	resp, err := through(u).Get("http://allowed.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	resp, err = through(u).Get("http://blocked.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()

	entries := srv.Log().Recent(0)
	if len(entries) != 2 {
		t.Fatalf("logged %d decisions, want 2: %+v", len(entries), entries)
	}
	if !entries[0].Allowed || entries[0].Target.Host != "allowed.test" {
		t.Errorf("first entry = %+v", entries[0])
	}
	if entries[1].Allowed || entries[1].Target.Host != "blocked.test" {
		t.Errorf("second entry = %+v", entries[1])
	}
	if entries[1].Reason == "" {
		t.Error("a denial with no reason gives the dashboard nothing to show")
	}
}

func TestUnresolvableHostIsReportedNotHung(t *testing.T) {
	srv := NewServer(
		func() Policy { return Policy{Preset: PresetOpen} },
		WithLookup(func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("no such host")
		}),
	)
	listener := httptest.NewServer(srv)
	defer listener.Close()
	u, _ := url.Parse(listener.URL)

	resp, err := through(u).Get("http://nowhere.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Error("an unresolvable host should not succeed")
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		authority string
		def       int
		host      string
		port      int
		wantErr   bool
	}{
		{"example.com:443", 80, "example.com", 443, false},
		{"example.com", 80, "example.com", 80, false},
		{"example.com", 443, "example.com", 443, false},
		{"[2001:db8::1]:443", 80, "2001:db8::1", 443, false},
		{"", 80, "", 0, true},
		{"example.com:0", 80, "", 0, true},
		{"example.com:99999", 80, "", 0, true},
	}
	for _, tc := range cases {
		got, err := parseTarget(tc.authority, tc.def)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseTarget(%q) should have failed", tc.authority)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTarget(%q): %v", tc.authority, err)
			continue
		}
		if got.Host != tc.host || got.Port != tc.port {
			t.Errorf("parseTarget(%q) = %v, want %s:%d", tc.authority, got, tc.host, tc.port)
		}
	}
}
