package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	probeInterval = time.Second
	probeTimeout  = 900 * time.Millisecond
	webTimeout    = 2 * time.Second

	internetHost = "1.1.1.1"
	// Cloudflare's connectivity-check endpoint, on the same network as
	// internetHost so Internet and Web differ only by protocol layer.
	webURL = "http://cp.cloudflare.com/generate_204"
)

var errCaptive = errors.New("captive portal")
var errNoRoute = errors.New("no route")

// dnsNames are rotated so no single record dominates; the trailing dot stops
// search domains being appended.
var dnsNames = []string{"apple.com.", "google.com.", "cloudflare.com.", "wikipedia.org."}

// pinger sends ICMP echo requests over an unprivileged datagram socket,
// which macOS permits without root. One socket is shared by all targets.
//
// macOS delivers every echo reply to every ICMP datagram socket, including
// replies to other processes' pings, so a reply is accepted only if it has
// our ID, our per-process token and comes from the address we pinged.
type pinger struct {
	conn  *icmp.PacketConn
	id    int
	token []byte
	mu    sync.Mutex
	seq   uint16
	wait  map[uint16]waiter
}

type waiter struct {
	peer net.IP
	ch   chan struct{}
}

func newPinger() (*pinger, error) {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	token := make([]byte, 8)
	rand.Read(token)
	p := &pinger{
		conn:  c,
		id:    os.Getpid() & 0xffff,
		token: append([]byte("gotnet"), token...),
		wait:  map[uint16]waiter{},
	}
	go p.read()
	return p, nil
}

func (p *pinger) read() {
	buf := make([]byte, 1500)
	for {
		n, from, err := p.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		b := buf[:n]
		// Darwin may deliver the IPv4 header on datagram ICMP sockets.
		if n >= 20 && b[0]>>4 == 4 {
			if ihl := int(b[0]&0x0f) * 4; ihl <= n {
				b = b[ihl:]
			}
		}
		m, err := icmp.ParseMessage(1, b)
		if err != nil || m.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := m.Body.(*icmp.Echo)
		if !ok || echo.ID != p.id || !bytes.Equal(echo.Data, p.token) {
			continue
		}
		addr, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		p.mu.Lock()
		if w, ok := p.wait[uint16(echo.Seq)]; ok && w.peer.Equal(addr.IP) {
			close(w.ch)
			delete(p.wait, uint16(echo.Seq))
		}
		p.mu.Unlock()
	}
}

func (p *pinger) ping(ctx context.Context, ip net.IP) (time.Duration, error) {
	p.mu.Lock()
	p.seq++
	seq := p.seq
	ch := make(chan struct{})
	p.wait[seq] = waiter{peer: ip, ch: ch}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.wait, seq)
		p.mu.Unlock()
	}()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: p.id, Seq: int(seq), Data: p.token},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := p.conn.WriteTo(b, &net.UDPAddr{IP: ip}); err != nil {
		return 0, err
	}
	select {
	case <-ch:
		return time.Since(start), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// tcpPing times a TCP handshake. A refused connection still proves the host
// answered, so it counts as a success. Used when ICMP is unavailable.
func tcpPing(ctx context.Context, ip net.IP, port string) (time.Duration, error) {
	var d net.Dialer
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
	rtt := time.Since(start)
	if err == nil {
		c.Close()
		return rtt, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return rtt, nil
	}
	return 0, err
}

// prober runs the four timed probes. The network snapshot is updated by the
// UI loop so probes follow the current gateway and nameserver.
type prober struct {
	ping   *pinger // nil when ICMP sockets are unavailable
	mu     sync.Mutex
	net    netInfo
	dnsIdx int
	web    *http.Client
}

func newProber() *prober {
	p := &prober{
		web: &http.Client{
			Timeout: webTimeout,
			// Fresh connections each time, and no proxy: we want to know
			// whether this machine can reach the web directly.
			Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	p.ping, _ = newPinger()
	return p
}

func (p *prober) setNet(n netInfo) {
	p.mu.Lock()
	p.net = n
	p.mu.Unlock()
}

func (p *prober) snapshot() netInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.net
}

func (p *prober) reach(ctx context.Context, ip net.IP, port string) (time.Duration, error) {
	if p.ping != nil {
		return p.ping.ping(ctx, ip)
	}
	return tcpPing(ctx, ip, port)
}

func (p *prober) probe(t target) (time.Duration, error) {
	timeout := probeTimeout
	if t == targetWeb {
		timeout = webTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	n := p.snapshot()

	switch t {
	case targetRouter:
		if n.gateway == nil {
			return 0, errNoRoute
		}
		return p.reach(ctx, n.gateway, "80")
	case targetInternet:
		return p.reach(ctx, net.ParseIP(internetHost), "443")
	case targetDNS:
		return p.dns(ctx, n.nameserver)
	case targetWeb:
		return p.fetch(ctx)
	}
	return 0, errors.New("unknown target")
}

func (p *prober) dns(ctx context.Context, server string) (time.Duration, error) {
	p.mu.Lock()
	name := dnsNames[p.dnsIdx%len(dnsNames)]
	p.dnsIdx++
	p.mu.Unlock()

	// Query the configured server directly with Go's resolver, which does no
	// caching, so every probe is a real round trip.
	r := net.DefaultResolver
	if server != "" {
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
			},
		}
	}
	start := time.Now()
	_, err := r.LookupNetIP(ctx, "ip4", name)
	return time.Since(start), err
}

func (p *prober) fetch(ctx context.Context) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := p.web.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	rtt := time.Since(start)
	if err != nil {
		return 0, err
	}
	// The endpoint returns an empty 204. Anything else, typically a 200
	// login page or a redirect, means something intercepted the request.
	if resp.StatusCode != http.StatusNoContent {
		return rtt, errCaptive
	}
	return rtt, nil
}
