// SPDX-License-Identifier: AGPL-3.0-or-later

// Package clientip finds the address of the person behind a request, believing
// forwarding headers only from proxies the operator named.
//
// X-Forwarded-For and X-Forwarded-Proto are claims any client can make. They
// carry weight only when the connection itself comes from a trusted proxy, and
// even then only the entries that trusted proxies appended: everything to the
// left of the first untrusted hop was supplied by that hop and cannot establish
// identity. An empty trust list therefore means no forwarding header is ever
// believed, including on requests from loopback.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrusted reads a comma-separated list of proxy addresses and CIDR
// prefixes, such as "127.0.0.1/32, ::1". A bare address means only that
// address. IPv4-mapped IPv6 forms are converted to IPv4, and prefixes are
// masked, so 10.1.2.3/8 becomes 10.0.0.0/8. An empty or blank value trusts no
// proxy. settingName names the setting in error messages, for example
// "WITMOOT_TRUSTED_PROXIES".
func ParseTrusted(value, settingName string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var proxies []netip.Prefix
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		prefix, err := parsePrefix(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", settingName, err)
		}
		proxies = append(proxies, prefix)
	}
	return proxies, nil
}

func parsePrefix(entry string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(entry)
	if err != nil {
		addr, addrErr := netip.ParseAddr(entry)
		// A zone names an interface on this host, which a prefix cannot
		// match, so an entry with one could never take effect.
		if addrErr != nil || addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("invalid proxy IP or CIDR %q", entry)
		}
		addr = addr.Unmap()
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if prefix.Addr().Is4In6() {
		// Requests from mapped addresses are unmapped before matching, so
		// the prefix has to be unmapped too.
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("use an IPv4 CIDR for %q", entry)
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked(), nil
}

// Resolver applies one trust list to requests. The zero value trusts no proxy.
type Resolver struct {
	// Trusted lists the proxies whose forwarding headers are believed,
	// usually from ParseTrusted.
	Trusted []netip.Prefix
}

// Client returns the address of the client behind r.
//
// It starts from the connection's peer. When the peer is a trusted proxy, it
// walks X-Forwarded-For from the right, passing over trusted proxies, and
// returns the first address that is not one. All header fields count as one
// list, in order. A hop that is not a plain IP address (empty, carrying a
// port or a zone, or anything else) ends the walk at the peer, so a broken
// or hostile chain can never pick an address. The result is never an
// IPv4-mapped IPv6 address. If the peer address itself does not parse, the
// zero Addr is returned; NetworkKey gives that a key of its own.
func (res Resolver) Client(r *http.Request) netip.Addr {
	peer, ok := peerAddr(r)
	if !ok {
		return netip.Addr{}
	}
	if !res.trusts(peer) {
		return peer
	}
	client := peer
	values := r.Header.Values("X-Forwarded-For")
	for field := len(values) - 1; field >= 0; field-- {
		rest := values[field]
		for {
			hop := rest
			comma := strings.LastIndexByte(rest, ',')
			if comma >= 0 {
				hop, rest = rest[comma+1:], rest[:comma]
			}
			addr, err := netip.ParseAddr(strings.TrimSpace(hop))
			if err != nil || addr.Zone() != "" {
				return peer
			}
			client = addr.Unmap()
			if !res.trusts(client) {
				return client
			}
			if comma < 0 {
				break
			}
		}
	}
	// Every hop was a trusted proxy, so the left-most is the best answer.
	return client
}

// ForwardedHTTPS reports whether r reached the first proxy over HTTPS: either
// the connection is TLS itself, or a trusted proxy said so in
// X-Forwarded-Proto. Only the right-most value counts, because that is the one
// the nearest proxy set; anything before it may have come from the client.
func (res Resolver) ForwardedHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, ok := peerAddr(r)
	if !ok || !res.trusts(peer) {
		return false
	}
	values := r.Header.Values("X-Forwarded-Proto")
	if len(values) == 0 {
		return false
	}
	last := values[len(values)-1]
	if comma := strings.LastIndexByte(last, ','); comma >= 0 {
		last = last[comma+1:]
	}
	return strings.EqualFold(strings.TrimSpace(last), "https")
}

// NetworkKey groups addresses for rate limiting. An IPv4 address is its own
// key. An IPv6 address is keyed by its /64, the smallest block normally
// assigned to one site, so one host cannot claim a fresh budget for every
// address it owns. Zones and IPv4 mapping are ignored. The zero Addr has the
// key "unknown".
func NetworkKey(addr netip.Addr) string {
	if !addr.IsValid() {
		return "unknown"
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

func (res Resolver) trusts(addr netip.Addr) bool {
	for _, prefix := range res.Trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerAddr parses the connection's address. A peer with a zone keeps it, and
// so is never trusted: prefixes do not match zoned addresses.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
