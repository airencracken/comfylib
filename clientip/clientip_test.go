// SPDX-License-Identifier: AGPL-3.0-or-later

package clientip

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

func TestParseTrusted(t *testing.T) {
	proxies, err := ParseTrusted(" 127.0.0.1, ::1/128, 10.1.2.3/8, ::ffff:192.0.2.1/120, ::ffff:198.51.100.7, 2001:db8::1/48 ", "TEST_PROXIES")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8", "192.0.2.0/24", "198.51.100.7/32", "2001:db8::/48"}
	if len(proxies) != len(want) {
		t.Fatalf("parsed %v", proxies)
	}
	for i := range want {
		if proxies[i].String() != want[i] {
			t.Errorf("proxy %d = %s; want %s", i, proxies[i], want[i])
		}
	}
	for _, empty := range []string{"", " ", "\t\n"} {
		if proxies, err := ParseTrusted(empty, "TEST_PROXIES"); err != nil || len(proxies) != 0 {
			t.Fatalf("%q should trust no proxies: %v %v", empty, proxies, err)
		}
	}
}

func TestParseTrustedRejectsAnythingAmbiguous(t *testing.T) {
	for _, invalid := range []string{
		"localhost", "127.0.0.1:8080", "127.0.0.1,", ",127.0.0.1", "127.0.0.1,,::1",
		"127.0.0.1/33", "::1/129", "fe80::1%eth0", "fe80::1%eth0/64", "::ffff:127.0.0.1/80",
		"127.0.0.1/", "/8", "10.0.0.0/8 10.1.0.0/16", "10.0.0.0/-1", "0x7f.0.0.1", "127.1",
		"[::1]", "127.0.0.1;", "127.0.0.1\x00", "１２７.0.0.1",
	} {
		if proxies, err := ParseTrusted(invalid, "TEST_PROXIES"); err == nil {
			t.Errorf("accepted %q as %v", invalid, proxies)
		} else if !strings.HasPrefix(err.Error(), "TEST_PROXIES: ") {
			t.Errorf("error does not name the setting: %v", err)
		}
	}
}

func request(peer string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = peer
	for _, value := range forwarded {
		r.Header.Add("X-Forwarded-For", value)
	}
	return r
}

func resolver(t *testing.T, value string) Resolver {
	t.Helper()
	proxies, err := ParseTrusted(value, "TEST_PROXIES")
	if err != nil {
		t.Fatal(err)
	}
	return Resolver{Trusted: proxies}
}

// The union of witmoot's trust-boundary cases and imvault's right-most-entry
// cases, rewritten for an explicit trust list.
func TestClientTrustBoundary(t *testing.T) {
	res := resolver(t, "127.0.0.1,::1,10.0.0.0/8")
	for _, tc := range []struct {
		name, peer string
		headers    []string
		want       string
	}{
		{"direct ignores spoof", "192.0.2.9:2345", []string{"198.51.100.1"}, "192.0.2.9"},
		{"local proxy", "127.0.0.1:2345", []string{"198.51.100.1"}, "198.51.100.1"},
		{"IPv6 proxy and client", "[::1]:2345", []string{"2001:db8::1234"}, "2001:db8::1234"},
		{"normalize mapped peer", "[::ffff:127.0.0.1]:2345", []string{"::ffff:198.51.100.1"}, "198.51.100.1"},
		{"ignore spoofed left entry", "127.0.0.1:2345", []string{"203.0.113.6, 198.51.100.1"}, "198.51.100.1"},
		{"trusted chain", "127.0.0.1:2345", []string{"203.0.113.6, 198.51.100.1, 10.2.3.4"}, "198.51.100.1"},
		{"multiple header fields", "127.0.0.1:2345", []string{"203.0.113.6", "198.51.100.1, 10.2.3.4"}, "198.51.100.1"},
		{"walk crosses header fields", "127.0.0.1:2345", []string{"198.51.100.1", "10.2.3.4"}, "198.51.100.1"},
		{"spaces", "127.0.0.1:2345", []string{" 198.51.100.7 ,  203.0.113.9 "}, "203.0.113.9"},
		{"client in trusted range", "127.0.0.1:2345", []string{"10.3.4.5"}, "10.3.4.5"},
		{"all hops trusted", "127.0.0.1:2345", []string{"10.3.4.5, 10.9.9.9"}, "10.3.4.5"},
		{"missing header", "127.0.0.1:2345", nil, "127.0.0.1"},
		{"empty header", "127.0.0.1:2345", []string{""}, "127.0.0.1"},
		{"malformed nearest hop", "127.0.0.1:2345", []string{"198.51.100.1, garbage"}, "127.0.0.1"},
		{"malformed middle hop", "127.0.0.1:2345", []string{"198.51.100.1, garbage, 10.2.3.4"}, "127.0.0.1"},
		{"malformed hop beyond the client", "127.0.0.1:2345", []string{"garbage, 198.51.100.1"}, "198.51.100.1"},
		{"trailing comma", "127.0.0.1:2345", []string{"198.51.100.1,"}, "127.0.0.1"},
		{"empty middle entry", "127.0.0.1:2345", []string{"198.51.100.1,,10.2.3.4"}, "127.0.0.1"},
		{"port is not an IP", "127.0.0.1:2345", []string{"198.51.100.1:1234"}, "127.0.0.1"},
		{"bracketed IPv6 is not an IP", "127.0.0.1:2345", []string{"[2001:db8::1]"}, "127.0.0.1"},
		{"zone is not accepted", "127.0.0.1:2345", []string{"fe80::1%eth0"}, "127.0.0.1"},
		{"hostname is not accepted", "127.0.0.1:2345", []string{"attacker.example"}, "127.0.0.1"},
		{"unknown is not accepted", "127.0.0.1:2345", []string{"unknown"}, "127.0.0.1"},
		{"obfuscated v4 is not accepted", "127.0.0.1:2345", []string{"0x7f000001"}, "127.0.0.1"},
		{"control characters", "127.0.0.1:2345", []string{"198.51.100.1\r\nX-Evil: 1"}, "127.0.0.1"},
		{"mapped trusted hop is trusted", "127.0.0.1:2345", []string{"198.51.100.1, ::ffff:10.2.3.4"}, "198.51.100.1"},
		{"untrusted peer with mapped form", "[::ffff:192.0.2.9]:2345", []string{"198.51.100.1"}, "192.0.2.9"},
		{"peer without port", "127.0.0.1", []string{"198.51.100.1"}, "198.51.100.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := res.Client(request(tc.peer, tc.headers...))
			if got.String() != tc.want {
				t.Fatalf("client = %s; want %s", got, tc.want)
			}
			if got.Is4In6() {
				t.Fatal("client address is still IPv4-mapped")
			}
		})
	}
}

// With no trust list, as with imvault's default, no header is ever read, even
// from loopback.
func TestUntrustedPeersHeadersAreIgnored(t *testing.T) {
	var none Resolver
	narrow := resolver(t, "10.0.0.1")
	for _, peer := range []string{"127.0.0.1:1", "[::1]:1", "10.0.0.2:1", "192.0.2.1:1", "[2001:db8::1]:1", "[fe80::1%eth0]:1"} {
		for _, res := range []Resolver{none, narrow} {
			r := request(peer, "198.51.100.7", "203.0.113.9")
			r.Header.Set("X-Real-IP", "198.51.100.8")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("Forwarded", "for=198.51.100.9;proto=https")
			want, _ := peerAddr(r)
			if got := res.Client(r); got != want {
				t.Errorf("peer %s with trust %v: client = %s", peer, res.Trusted, got)
			}
			if res.ForwardedHTTPS(r) {
				t.Errorf("peer %s with trust %v: believed X-Forwarded-Proto", peer, res.Trusted)
			}
		}
	}
}

// A link-local peer carries a zone. Prefixes never match zoned addresses, so
// such a peer cannot be trusted even inside a trusted range.
func TestZonedPeersAreNeverTrusted(t *testing.T) {
	res := resolver(t, "fe80::/10")
	got := res.Client(request("[fe80::1%eth0]:443", "198.51.100.1"))
	if got.String() != "fe80::1%eth0" {
		t.Fatalf("client = %s", got)
	}
}

func TestUnparseablePeersFallBackToTheZeroAddress(t *testing.T) {
	res := resolver(t, "0.0.0.0/0,::/0")
	for _, peer := range []string{"", "@", "/run/app.sock", "not-an-ip:80", "[::1", "1.2.3.4:5:6"} {
		r := request(peer, "198.51.100.1")
		if got := res.Client(r); got.IsValid() {
			t.Errorf("peer %q gave client %s", peer, got)
		}
		if res.ForwardedHTTPS(r) {
			t.Errorf("peer %q was trusted for X-Forwarded-Proto", peer)
		}
		if key := NetworkKey(res.Client(r)); key != "unknown" {
			t.Errorf("peer %q got key %q", peer, key)
		}
	}
}

func TestHugeForwardingChainsAreCheap(t *testing.T) {
	res := resolver(t, "127.0.0.1,10.0.0.0/8")
	// A megabyte of attacker-chosen hops to the left of the real client.
	chain := strings.Repeat("10.1.1.1, ", 100000) + "198.51.100.1, 10.2.2.2"
	r := request("127.0.0.1:1", chain)
	started := time.Now()
	if got := res.Client(r); got.String() != "198.51.100.1" {
		t.Fatalf("client = %s", got)
	}
	// Even a chain made only of trusted hops must be walked in linear time.
	all := request("127.0.0.1:1", strings.Repeat("10.1.1.1,", 100000)+"10.1.1.2")
	if got := res.Client(all); got.String() != "10.1.1.1" {
		t.Fatalf("all-trusted client = %s", got)
	}
	fields := request("127.0.0.1:1")
	for range 10000 {
		fields.Header.Add("X-Forwarded-For", "10.3.3.3")
	}
	if got := res.Client(fields); got.String() != "10.3.3.3" {
		t.Fatalf("many-field client = %s", got)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("walking hostile chains took %s", elapsed)
	}
	allocs := testing.AllocsPerRun(10, func() { res.Client(r) })
	if allocs > 2 {
		t.Fatalf("a walk allocated %.0f times; it should not copy the header", allocs)
	}
}

func TestForwardedHTTPS(t *testing.T) {
	res := resolver(t, "127.0.0.1")
	for _, tc := range []struct {
		name, peer string
		proto      []string
		tls        bool
		want       bool
	}{
		{"direct TLS", "192.0.2.1:1", nil, true, true},
		{"direct TLS ignores header", "192.0.2.1:1", []string{"http"}, true, true},
		{"plain direct", "192.0.2.1:1", nil, false, false},
		{"spoofed by a client", "192.0.2.1:1", []string{"https"}, false, false},
		{"trusted proxy says https", "127.0.0.1:1", []string{"https"}, false, true},
		{"case does not matter", "127.0.0.1:1", []string{"HTTPS"}, false, true},
		{"trusted proxy says http", "127.0.0.1:1", []string{"http"}, false, false},
		{"no header", "127.0.0.1:1", nil, false, false},
		{"right-most value wins", "127.0.0.1:1", []string{"https, http"}, false, false},
		{"right-most field wins", "127.0.0.1:1", []string{"http", "https"}, false, true},
		{"client prepends https", "127.0.0.1:1", []string{"https", "http"}, false, false},
		{"spaces", "127.0.0.1:1", []string{" http ,  https "}, false, true},
		{"lookalike", "127.0.0.1:1", []string{"https2"}, false, false},
		{"prefix", "127.0.0.1:1", []string{"httpsx"}, false, false},
		{"empty", "127.0.0.1:1", []string{""}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.peer
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			for _, value := range tc.proto {
				r.Header.Add("X-Forwarded-Proto", value)
			}
			if got := res.ForwardedHTTPS(r); got != tc.want {
				t.Fatalf("ForwardedHTTPS = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestNetworkKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1":               "192.0.2.1",
		"::ffff:192.0.2.1":        "192.0.2.1",
		"2001:db8:1:2:3:4:5:6":    "2001:db8:1:2::/64",
		"2001:db8:1:2::":          "2001:db8:1:2::/64",
		"2001:db8:1:3::1":         "2001:db8:1:3::/64",
		"fe80::1%eth0":            "fe80::/64",
		"::1":                     "::/64",
		"::":                      "::/64",
		"64:ff9b::192.0.2.1":      "64:ff9b::/64",
		"2001:db8:ffff:ffff:ff::": "2001:db8:ffff:ffff::/64",
	} {
		if got := NetworkKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("NetworkKey(%s) = %s; want %s", in, got, want)
		}
	}
	if got := NetworkKey(netip.Addr{}); got != "unknown" {
		t.Errorf("NetworkKey(zero) = %s", got)
	}
}

// Every address in one /64 shares a key, and neighbouring /64s never do.
func TestNetworkKeyIsStableWithinA64(t *testing.T) {
	property := func(network [8]byte, a, b [8]byte, flip uint8) bool {
		var first, second, neighbour [16]byte
		copy(first[:8], network[:])
		copy(first[8:], a[:])
		copy(second[:8], network[:])
		copy(second[8:], b[:])
		neighbour = first
		neighbour[flip%8] ^= 1 << (flip / 8 % 8)
		x, y, z := netip.AddrFrom16(first), netip.AddrFrom16(second), netip.AddrFrom16(neighbour)
		if x.Is4In6() || z.Is4In6() || y.Is4In6() {
			return true // mapped addresses are keyed as IPv4 instead
		}
		return NetworkKey(x) == NetworkKey(y) && NetworkKey(x) != NetworkKey(z) &&
			NetworkKey(x) == NetworkKey(x.WithZone("eth0"))
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
	ipv4 := func(a, b [4]byte) bool {
		x, y := netip.AddrFrom4(a), netip.AddrFrom4(b)
		mapped := netip.AddrFrom16(x.As16())
		return NetworkKey(x) == NetworkKey(mapped) && (a == b) == (NetworkKey(x) == NetworkKey(y))
	}
	if err := quick.Check(ipv4, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func FuzzParseTrusted(f *testing.F) {
	for _, seed := range []string{"", "127.0.0.1", "::1/128, 10.0.0.0/8", "::ffff:1.2.3.4/120", "fe80::1%eth0", "1.2.3.4/33", ",", "::ffff:0:0/96"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		proxies, err := ParseTrusted(value, "TEST_PROXIES")
		if err != nil {
			if proxies != nil {
				t.Fatal("an error came with prefixes")
			}
			return
		}
		if strings.TrimSpace(value) != "" && len(proxies) != strings.Count(value, ",")+1 {
			t.Fatalf("%q parsed into %d prefixes", value, len(proxies))
		}
		for _, prefix := range proxies {
			if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
				t.Fatalf("%q produced unusable prefix %v", value, prefix)
			}
			// Every accepted prefix must trust at least its own address.
			if !(Resolver{Trusted: proxies}).trusts(prefix.Addr()) {
				t.Fatalf("%v does not contain its own address", prefix)
			}
		}
	})
}

func FuzzClient(f *testing.F) {
	f.Add("127.0.0.1:1", "203.0.113.6, 198.51.100.1, 10.2.3.4", "10.0.0.1")
	f.Add("[::1]:1", "2001:db8::1, garbage", "")
	f.Add("192.0.2.1:1", "198.51.100.1", "::ffff:10.2.3.4")
	f.Add("[::ffff:127.0.0.1]:1", ",,,", "fe80::1%eth0")
	f.Fuzz(func(t *testing.T, peer, first, second string) {
		res := Resolver{Trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("10.0.0.0/8")}}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = peer
		r.Header.Add("X-Forwarded-For", first)
		r.Header.Add("X-Forwarded-For", second)
		got := res.Client(r)
		peerIP, ok := peerAddr(r)
		if !ok {
			if got.IsValid() {
				t.Fatalf("unparseable peer %q gave %s", peer, got)
			}
			return
		}
		if got.Is4In6() {
			t.Fatalf("returned a mapped address %s", got)
		}
		if !res.trusts(peerIP) {
			if got != peerIP {
				t.Fatalf("untrusted peer %s let the header choose %s", peerIP, got)
			}
			return
		}
		if got == peerIP {
			return
		}
		// Anything other than the peer must be one of the hops, and the
		// header must contain it in some spelling.
		found := false
		for _, hop := range strings.Split(first+","+second, ",") {
			if addr, err := netip.ParseAddr(strings.TrimSpace(hop)); err == nil && addr.Unmap() == got {
				found = true
			}
		}
		if !found {
			t.Fatalf("client %s does not appear in %q / %q", got, first, second)
		}
		if NetworkKey(got) == "" {
			t.Fatal("empty network key")
		}
	})
}
