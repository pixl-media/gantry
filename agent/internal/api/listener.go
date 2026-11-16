package api

import (
	"net"
	"net/netip"
	"sync"
)

// Connection limits: plenty for browsers and API clients, but a crowd of
// idle or slow connections can't use up the agent's file descriptors, which
// it needs for the Docker socket and Compose.
const (
	maxConns        = 1024
	maxConnsPerHost = 128
)

// Listen listens on addr and limits concurrent connections: in total, and
// per client address (IPv6 per /64). Trusted proxies, which carry many
// clients' requests, only count toward the total.
func Listen(addr string, trusted []netip.Prefix) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return newLimitListener(ln, trusted, maxConns, maxConnsPerHost), nil
}

type limitListener struct {
	net.Listener
	net           network
	total, perKey int

	mu     sync.Mutex
	open   int
	byHost map[string]int
}

func newLimitListener(ln net.Listener, trusted []netip.Prefix, total, perHost int) *limitListener {
	return &limitListener{
		Listener: ln, net: network{trusted: trusted},
		total: total, perKey: perHost, byHost: map[string]int{},
	}
}

// Accept returns the next connection within the limits; connections beyond
// them are closed at once.
func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		var addr netip.Addr
		if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err == nil {
			addr = ap.Addr().Unmap()
		}
		key := ""
		if !l.net.isTrusted(addr) {
			key = limitKey(addr)
		}
		if l.acquire(key) {
			return &limitedConn{Conn: c, release: func() { l.release(key) }}, nil
		}
		c.Close()
	}
}

func (l *limitListener) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open >= l.total || (key != "" && l.byHost[key] >= l.perKey) {
		return false
	}
	l.open++
	if key != "" {
		l.byHost[key]++
	}
	return true
}

func (l *limitListener) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.open--
	if key != "" {
		if l.byHost[key]--; l.byHost[key] <= 0 {
			delete(l.byHost, key)
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
