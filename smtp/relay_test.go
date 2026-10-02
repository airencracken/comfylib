// SPDX-License-Identifier: AGPL-3.0-or-later

package smtp

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// session is everything one client connection did, captured by the fake relay.
type session struct {
	from string
	to   []string
	data string
	// commands lists every command verb in order, each prefixed with "tls:"
	// once the session was encrypted.
	commands []string
}

// fakeRelay is a small SMTP server: enough of the protocol to accept a message,
// optionally over real TLS, and hand back what happened.
type fakeRelay struct {
	listener net.Listener
	sessions chan session
	// advertiseStartTLS makes the relay offer STARTTLS. It is honoured only
	// when certificate is set; otherwise STARTTLS is refused.
	advertiseStartTLS bool
	certificate       *tls.Certificate
	// requireAuth rejects mail unless an AUTH exchange happened.
	requireAuth bool
}

type relayOption func(*fakeRelay)

func withStartTLS(cert *tls.Certificate) relayOption {
	return func(r *fakeRelay) { r.advertiseStartTLS, r.certificate = true, cert }
}

func withAuth() relayOption { return func(r *fakeRelay) { r.requireAuth = true } }

func newFakeRelay(t *testing.T, options ...relayOption) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return startRelay(t, listener, options...)
}

// newImplicitRelay speaks TLS from the first byte, as on port 465.
func newImplicitRelay(t *testing.T, cert *tls.Certificate) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return startRelay(t, tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{*cert}}))
}

func startRelay(t *testing.T, listener net.Listener, options ...relayOption) *fakeRelay {
	t.Helper()
	relay := &fakeRelay{listener: listener, sessions: make(chan session, 16)}
	for _, option := range options {
		option(relay)
	}
	go relay.serve()
	t.Cleanup(func() { closeTest(t, listener) })
	return relay
}

func (r *fakeRelay) config(mode TLSMode) Config {
	address := r.listener.Addr().(*net.TCPAddr)
	return Config{Host: "127.0.0.1", Port: address.Port, From: "Witmoot <no-reply@example.org>", Mode: mode}
}

func (r *fakeRelay) serve() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.handle(conn)
	}
}

// relayConn is one client connection to the fake relay.
type relayConn struct {
	relay     *fakeRelay
	conn      net.Conn
	reader    *bufio.Reader
	writer    *bufio.Writer
	encrypted bool
	authed    bool
	current   session
}

func (r *fakeRelay) handle(conn net.Conn) {
	_, encrypted := conn.(*tls.Conn)
	c := &relayConn{relay: r, conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn), encrypted: encrypted}
	// A session can outlive its test, so there is nowhere to report a failed
	// close; the client side observes any real problem.
	defer func() { _ = c.conn.Close() }()
	defer func() { r.sessions <- c.current }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c.reply("220 fake ESMTP ready")
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil || !c.command(strings.TrimSpace(line)) {
			return
		}
	}
}

// reply sends one line. A failed reply means the client has hung up, and the
// next read ends the session.
func (c *relayConn) reply(line string) {
	if _, err := c.writer.WriteString(line + "\r\n"); err == nil {
		_ = c.writer.Flush()
	}
}

// command handles one command and reports whether the session continues.
func (c *relayConn) command(command string) bool {
	upper := strings.ToUpper(command)
	verb, _, _ := strings.Cut(upper, " ")
	verb, _, _ = strings.Cut(verb, ":")
	if c.encrypted {
		verb = "tls:" + verb
	}
	c.current.commands = append(c.current.commands, verb)
	switch {
	case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
		c.reply("250-fake greets you")
		if c.relay.advertiseStartTLS && !c.encrypted {
			c.reply("250-STARTTLS")
		}
		c.reply("250 AUTH PLAIN")
	case upper == "STARTTLS":
		return c.startTLS()
	case strings.HasPrefix(upper, "AUTH"):
		c.authed = true
		c.reply("235 authenticated")
	case strings.HasPrefix(upper, "MAIL FROM:"):
		if c.relay.requireAuth && !c.authed {
			c.reply("530 authentication required")
			return true
		}
		c.current.from = strings.TrimSpace(command[len("MAIL FROM:"):])
		c.reply("250 ok")
	case strings.HasPrefix(upper, "RCPT TO:"):
		c.current.to = append(c.current.to, strings.TrimSpace(command[len("RCPT TO:"):]))
		c.reply("250 ok")
	case upper == "DATA":
		c.reply("354 end with <CRLF>.<CRLF>")
		body, err := textproto.NewReader(c.reader).ReadDotBytes()
		if err != nil {
			return false
		}
		c.current.data = string(body)
		c.reply("250 queued")
	case upper == "QUIT":
		c.reply("221 bye")
		return false
	default:
		c.reply("250 ok")
	}
	return true
}

func (c *relayConn) startTLS() bool {
	if c.relay.certificate == nil || c.encrypted {
		c.reply("454 TLS not available")
		return true
	}
	c.reply("220 go ahead")
	server := tls.Server(c.conn, &tls.Config{Certificates: []tls.Certificate{*c.relay.certificate}})
	if err := server.Handshake(); err != nil {
		c.current.commands = append(c.current.commands, "handshake-failed")
		return false
	}
	c.conn, c.encrypted = server, true
	c.reader, c.writer = bufio.NewReader(server), bufio.NewWriter(server)
	return true
}

// wait returns the next finished session, or fails.
func (r *fakeRelay) wait(t *testing.T) session {
	t.Helper()
	select {
	case s := <-r.sessions:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no session finished")
		return session{}
	}
}

// delivered returns the next session that carried a message, or fails.
func (r *fakeRelay) delivered(t *testing.T) session {
	t.Helper()
	s := r.wait(t)
	if s.data == "" {
		t.Fatalf("the session delivered nothing: %v", s.commands)
	}
	return s
}

// testCertificate returns a self-signed certificate for the given names and
// a pool that trusts it.
func testCertificate(t *testing.T, names ...string) (*tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake relay"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, name := range names {
		if addr, err := netip.ParseAddr(name); err == nil {
			template.IPAddresses = append(template.IPAddresses, addr.AsSlice())
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}, pool
}

// closeTest closes a test resource and reports a failure.
func closeTest(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}
