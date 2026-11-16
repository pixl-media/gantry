package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// network is how the agent sees its clients: which peers are trusted
// proxies, and whether credentials may come over plain HTTP from the
// internet.
type network struct {
	// trusted are the reverse proxies (e.g. the web UI's container) whose
	// X-Forwarded-For and X-Forwarded-Proto the agent believes.
	trusted []netip.Prefix
	// allowInsecureHTTP lets credentials from public addresses come over
	// plain HTTP (GANTRY_ALLOW_INSECURE_HTTP).
	allowInsecureHTTP bool
}

// ParseTrustedProxies parses GANTRY_TRUSTED_PROXIES: IP addresses or CIDR
// prefixes, separated by commas or spaces.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			prefixes = append(prefixes, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("GANTRY_TRUSTED_PROXIES: %q is neither an IP address nor a CIDR prefix", f)
		}
		a = a.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
	}
	return prefixes, nil
}

// peer returns the address of the direct peer of the connection.
func peer(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func (n network) isTrusted(a netip.Addr) bool {
	for _, p := range n.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// client returns the client's address: the peer, or, if the peer is a
// trusted proxy, the last address in X-Forwarded-For that isn't one.
// Proxies append the address they received the request from, so entries to
// the left of the first untrusted one could be forged by the client.
func (n network) client(r *http.Request) netip.Addr {
	p := peer(r)
	if !p.IsValid() || !n.isTrusted(p) {
		return p
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return p
		}
		if a = a.Unmap(); !n.isTrusted(a) {
			return a
		}
	}
	return p
}

// limitKey groups addresses the login limit counts together: an IPv4
// address, or the /64 network of an IPv6 address. A single connection
// usually gets a whole /64, so counting single IPv6 addresses would let an
// attacker rotate through billions of them.
func limitKey(a netip.Addr) string {
	if !a.IsValid() {
		return "unknown"
	}
	if a.Is4() {
		return a.String()
	}
	p, _ := a.Prefix(64)
	return p.String()
}

// sharedAddressSpace is 100.64.0.0/10, used by carrier-grade NAT and by VPNs
// such as Tailscale.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// public reports whether a is an internet address: not loopback, private
// (10/8, 172.16/12, 192.168/16, fc00::/7), link-local or shared address
// space.
func public(a netip.Addr) bool {
	return a.IsValid() && !a.IsLoopback() && !a.IsPrivate() && !a.IsLinkLocalUnicast() &&
		!a.IsUnspecified() && !sharedAddressSpace.Contains(a)
}

// secureTransport reports whether the request came over HTTPS: directly, or
// through a proxy that says so and is trusted or in a private network.
// X-Forwarded-Proto from anywhere else could be set by the client itself.
func (n network) secureTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return false
	}
	p := peer(r)
	return n.isTrusted(p) || (p.IsValid() && !public(p))
}

// insecure reports whether credentials in r must be refused: they came over
// plain HTTP from an internet address, so anyone on the way could read
// them, and the operator hasn't allowed that.
func (n network) insecure(r *http.Request) bool {
	if n.allowInsecureHTTP || n.secureTransport(r) {
		return false
	}
	return public(n.client(r))
}
