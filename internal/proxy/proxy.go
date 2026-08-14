package proxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dialTimeout caps how long an allowed request waits for its upstream.
const dialTimeout = 30 * time.Second

// ErrBlocked means every address a name resolved to is one a sandbox may not
// reach. It is separate from a policy denial: no rule can permit these.
var ErrBlocked = errors.New("resolves only to addresses a sandbox may not reach")

// Server is the filtering forward proxy. Sandboxes reach it as their only way
// out, and it decides every request against the policy.
type Server struct {
	// policy is read per request rather than held, so `plbx policy allow`
	// takes effect on the next connection instead of the next daemon.
	policy func() Policy
	log    *Log

	// lookup, dial, and allowAddr are injectable so tests can stand up a real
	// proxy against a loopback upstream, which the address guard would
	// otherwise refuse on sight.
	lookup    func(ctx context.Context, host string) ([]netip.Addr, error)
	dial      func(ctx context.Context, addr string) (net.Conn, error)
	allowAddr func(netip.Addr) bool
}

// Option configures a [Server].
type Option func(*Server)

// WithLog replaces the connection log.
func WithLog(l *Log) Option { return func(s *Server) { s.log = l } }

// WithLookup replaces name resolution.
func WithLookup(fn func(context.Context, string) ([]netip.Addr, error)) Option {
	return func(s *Server) { s.lookup = fn }
}

// WithDialer replaces how upstream connections are made.
func WithDialer(fn func(context.Context, string) (net.Conn, error)) Option {
	return func(s *Server) { s.dial = fn }
}

// WithAddressGuard replaces the check that refuses private space. Only tests
// should use it: relaxing it in production puts the host's own network, and
// every other sandbox, one request away.
func WithAddressGuard(fn func(netip.Addr) bool) Option {
	return func(s *Server) { s.allowAddr = fn }
}

// NewServer returns a proxy enforcing whatever policy returns.
func NewServer(policy func() Policy, opts ...Option) *Server {
	s := &Server{
		policy:    policy,
		log:       NewLog(defaultLogLimit),
		allowAddr: AllowsAddress,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "tcp", addr)
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Log is the proxy's record of what it decided.
func (s *Server) Log() *Log { return s.log }

// ServeHTTP handles both shapes a forward proxy sees: CONNECT for anything
// tunnelled, and an absolute-URI request for plain HTTP.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.serveConnect(w, r)
		return
	}
	s.serveHTTP(w, r)
}

// serveConnect decides on a tunnel, then hands the connection over to it.
func (s *Server) serveConnect(w http.ResponseWriter, r *http.Request) {
	target, err := parseTarget(r.Host, 443)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	upstream, verdict, err := s.open(r.Context(), target, sandboxOf(r))
	if err != nil {
		refuse(w, target, verdict, err)
		return
	}
	defer func() { _ = upstream.Close() }()

	// take the raw connection: from here it carries TLS we do not read.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy cannot take over the connection", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = client.Close() }()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	tunnel(client, upstream)
}

// serveHTTP forwards a plain request, which arrives whole rather than as an
// opaque tunnel.
func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "expected an absolute URI, which is what a proxy is asked for", http.StatusBadRequest)
		return
	}
	target, err := parseTarget(r.Host, 80)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	upstream, verdict, err := s.open(r.Context(), target, sandboxOf(r))
	if err != nil {
		refuse(w, target, verdict, err)
		return
	}
	defer func() { _ = upstream.Close() }()

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	// hop-by-hop headers are ours, not the origin's.
	outbound.Header.Del("Proxy-Authorization")
	outbound.Header.Del("Proxy-Connection")

	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			// the connection is already open and already vetted; dialling
			// again here would skip the address guard.
			return upstream, nil
		},
		DisableKeepAlives: true,
	}
	resp, err := transport.RoundTrip(outbound)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// open checks a target against the policy, records the decision, and returns a
// vetted upstream connection.
//
// Resolution happens here rather than in the dialer because the policy names
// hosts while the guard covers addresses: a name that resolves into private
// space would otherwise pass a hostname check and land on the host's network.
func (s *Server) open(ctx context.Context, t Target, sandbox string) (net.Conn, Verdict, error) {
	verdict := s.policy().Check(t)
	if !verdict.Allowed {
		s.log.Record(Entry{Target: t, Allowed: false, Reason: verdict.Reason, Sandbox: sandbox})
		return nil, verdict, errors.New(verdict.Reason)
	}

	addrs, err := s.lookup(ctx, t.Host)
	if err != nil {
		s.log.Record(Entry{Target: t, Allowed: false, Reason: "cannot resolve: " + err.Error(), Sandbox: sandbox})
		return nil, verdict, err
	}

	var lastErr error
	for _, addr := range addrs {
		if !s.allowAddr(addr) {
			lastErr = ErrBlocked
			continue
		}
		conn, err := s.dial(ctx, net.JoinHostPort(addr.String(), strconv.Itoa(t.Port)))
		if err != nil {
			lastErr = err
			continue
		}
		s.log.Record(Entry{Target: t, Allowed: true, Reason: verdict.Reason, Sandbox: sandbox})
		return conn, verdict, nil
	}

	if lastErr == nil {
		lastErr = ErrBlocked
	}
	reason := verdict.Reason
	if errors.Is(lastErr, ErrBlocked) {
		reason = t.Host + " " + ErrBlocked.Error()
	}
	s.log.Record(Entry{Target: t, Allowed: false, Reason: reason, Sandbox: sandbox})
	return nil, Verdict{Reason: reason}, lastErr
}

// tunnel copies in both directions until either side is done.
func tunnel(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, client)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	wg.Wait()
}

// closeWrite tells the far side that this direction is finished, so a peer
// waiting on EOF stops waiting. Closing the whole connection instead would cut
// off the other direction mid-response.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// refuse answers a denied or failed request, saying which host and why.
func refuse(w http.ResponseWriter, t Target, v Verdict, err error) {
	reason := v.Reason
	if reason == "" {
		reason = err.Error()
	}
	status := http.StatusForbidden
	if v.Allowed {
		// the policy said yes and the connection still failed, which is the
		// upstream's problem rather than the sandbox's.
		status = http.StatusBadGateway
	}
	http.Error(w, "plbx: "+t.String()+": "+reason, status)
}

// parseTarget reads a "host" or "host:port" authority, defaulting the port.
func parseTarget(authority string, defaultPort int) (Target, error) {
	if authority == "" {
		return Target{}, errors.New("no host in the request")
	}
	// an authority with no port is normal for plain HTTP, and SplitHostPort
	// calls that an error. Deciding first keeps a genuinely malformed
	// authority from being read as a bare hostname.
	if !hasPort(authority) {
		return Target{Host: strings.Trim(authority, "[]"), Port: defaultPort}, nil
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return Target{}, fmt.Errorf("invalid host %q", authority)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return Target{}, fmt.Errorf("invalid port %q", port)
	}
	return Target{Host: strings.Trim(host, "[]"), Port: n}, nil
}

// sandboxOf reads which sandbox a request came from.
//
// Every sandbox is handed its own token in the proxy URL it is given, so the
// log can say who asked. Attribution only: the policy is host-side and the
// same for everyone, so a token proves nothing and grants nothing.
func sandboxOf(r *http.Request) string {
	const prefix = "Basic "
	auth := r.Header.Get("Proxy-Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	name, _, ok := parseBasic(auth[len(prefix):])
	if !ok {
		return ""
	}
	return name
}

// parseBasic decodes a Basic credential into its two halves.
func parseBasic(encoded string) (user, pass string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(raw), ":")
	return user, pass, ok
}

// hasPort reports whether an authority carries one. A bracketed address keeps
// its colons inside the brackets, so only what follows them counts; an
// unbracketed address with several colons is a bare IPv6 literal rather than a
// host and port.
func hasPort(authority string) bool {
	if i := strings.LastIndex(authority, "]"); i >= 0 {
		return strings.Contains(authority[i:], ":")
	}
	return strings.Count(authority, ":") == 1
}
