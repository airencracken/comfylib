// SPDX-License-Identifier: AGPL-3.0-or-later

package smtp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

const childEnv = "SMTP_TEST_CHILD"

// resetSecret stands in for a live reset link. It must never reach a log.
const resetSecret = "https://board.example.org/reset/live-token-5c1d"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "disabled" {
		os.Exit(childDisabled())
	}
	os.Exit(m.Run())
}

// childDisabled sends through Disabled with every logging route pointed at
// stderr, so the parent sees anything that is written anywhere.
func childDisabled() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(os.Stderr)
	err := Disabled{}.Send(context.Background(), Message{To: "a@example.org", Subject: "Reset " + resetSecret, Body: resetSecret})
	if !errors.Is(err, ErrDisabled) {
		fmt.Fprintln(os.Stderr, "unexpected error")
		return 1
	}
	return 0
}

func send(t *testing.T, cfg Config, msg Message) error {
	t.Helper()
	sender, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return sender.Send(t.Context(), msg)
}

func headersOf(t *testing.T, raw string) textproto.MIMEHeader {
	t.Helper()
	header, err := textproto.NewReader(bufio.NewReader(strings.NewReader(raw))).ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}
	return header
}

func TestSendDeliversThroughARelay(t *testing.T) {
	relay := newFakeRelay(t)
	sender, err := New(relay.config(TLSNone))
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Enabled() {
		t.Fatal("a configured sender should report as enabled")
	}
	if err := sender.Send(t.Context(), Message{
		To:      "alice@example.org",
		Subject: "Reset your password",
		Body:    "Open this link: https://board.example.org/reset/abc123\n",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := relay.delivered(t)
	if got.from != "<no-reply@example.org>" {
		t.Errorf("envelope sender = %q, want the address from the From header", got.from)
	}
	if len(got.to) != 1 || got.to[0] != "<alice@example.org>" {
		t.Errorf("recipients = %v", got.to)
	}
	header := headersOf(t, got.data)
	for name, want := range map[string]string{
		"From":                      `"Witmoot" <no-reply@example.org>`,
		"To":                        "alice@example.org",
		"Subject":                   "Reset your password",
		"Content-Type":              `text/plain; charset="utf-8"`,
		"Mime-Version":              "1.0",
		"Content-Transfer-Encoding": "8bit",
	} {
		if value := header.Get(name); value != want {
			t.Errorf("%s = %q, want %q", name, value, want)
		}
	}
	if _, err := time.Parse(time.RFC1123Z, header.Get("Date")); err != nil {
		t.Errorf("Date header: %v", err)
	}
	if !strings.Contains(got.data, "https://board.example.org/reset/abc123") {
		t.Errorf("message body is missing:\n%s", got.data)
	}
	if want := []string{"EHLO", "MAIL", "RCPT", "DATA", "QUIT"}; !slices.Equal(got.commands, want) {
		t.Errorf("commands = %v, want %v", got.commands, want)
	}
}

func TestSendAuthenticatesWhenCredentialsAreSet(t *testing.T) {
	relay := newFakeRelay(t, withAuth())
	cfg := relay.config(TLSNone)
	cfg.Username, cfg.Password = "relay-user", "relay-pass"
	// PlainAuth only hands over credentials on an unencrypted connection when
	// the relay is on the loopback interface, which this one is.
	if err := send(t, cfg, Message{To: "alice@example.org", Subject: "Hello", Body: "Body"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := relay.delivered(t); !slices.Contains(got.commands, "AUTH") {
		t.Errorf("the relay was never sent an AUTH exchange: %v", got.commands)
	}
}

func TestSendReportsDialFailure(t *testing.T) {
	// Nothing is listening on this port.
	err := send(t, Config{Host: "127.0.0.1", Port: 1, From: "no-reply@example.org", Mode: TLSNone, Timeout: time.Second},
		Message{To: "alice@example.org", Subject: "Hello", Body: "Body"})
	if err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatalf("expected a dial failure, got %v", err)
	}
}

func TestSendHonoursContextCancellation(t *testing.T) {
	relay := newFakeRelay(t)
	sender, err := New(relay.config(TLSNone))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sender.Send(ctx, Message{To: "b@example.org", Subject: "x", Body: "y"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a cancelled context to stop the send: %v", err)
	}
}

// Imvault's old Disabled sender logged the whole message, reset link and all.
// This one must refuse, and nothing may reach stdout, stderr, the standard
// logger or slog. The check runs in a child process so that output written
// any way at all is caught.
func TestDisabledSenderRefusesWithoutLogging(t *testing.T) {
	sender := Disabled{}
	if sender.Enabled() {
		t.Error("a disabled sender must not claim to be enabled")
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if err := sender.Send(t.Context(), Message{To: "a@example.org", Subject: "x", Body: resetSecret}); !errors.Is(err, ErrDisabled) {
		t.Errorf("a disabled sender must refuse: %v", err)
	}
	if logs.Len() != 0 {
		t.Fatalf("a disabled sender logged the message: %s", &logs)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=disabled")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("a disabled sender wrote output: %q", output)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	relay := newFakeRelay(t)
	sender, err := New(relay.config(TLSNone))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []Message{
		{To: "alice@example.org\r\nBcc: attacker@example.org", Subject: "Reset", Body: "x"},
		{To: "alice@example.org\nBcc: attacker@example.org", Subject: "Reset", Body: "x"},
		{To: "alice@example.org\r", Subject: "Reset", Body: "x"},
		{To: "alice@example.org", Subject: "Reset\r\nBcc: attacker@example.org", Body: "x"},
		{To: "alice@example.org", Subject: "Reset\nX-Injected: yes", Body: "x"},
		{To: "alice@example.org", Subject: "Reset\r", Body: "x"},
		{To: "", Subject: "Reset", Body: "x"},
		{To: "alice@example.org, attacker@example.org", Subject: "Reset", Body: "x"},
		{To: "Alice <alice@example.org>", Subject: "Reset", Body: "x"},
		{To: "alice@example.org>\r\nRCPT TO:<attacker@example.org", Subject: "Reset", Body: "x"},
		{To: "alice@example.org\x00", Subject: "Reset", Body: "x"},
		{To: "alice@example.org\t", Subject: "Reset", Body: "x"},
		{To: "alice @example.org", Subject: "Reset", Body: "x"},
	} {
		if err := sender.Send(t.Context(), msg); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("Send(%q, %q) = %v, want ErrInvalidMessage", msg.To, msg.Subject, err)
		}
	}
	// Refused messages never open a connection.
	select {
	case s := <-relay.sessions:
		t.Fatalf("a refused message reached the relay: %v", s.commands)
	case <-time.After(50 * time.Millisecond):
	}
}

// buildMessage strips line breaks itself, so a caller that skipped the checks
// in Send still could not add a header.
func TestBuiltMessagesCannotCarryInjectedHeaders(t *testing.T) {
	raw := string(buildMessage("no-reply@example.org\r\nBcc: a@example.org", "no-reply@example.org", Message{
		To:      "alice@example.org\r\nBcc: attacker@example.org",
		Subject: "Reset\r\nX-Injected: yes",
		Body:    "Body",
	}, time.Now()))
	head, body, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatal("no header/body separator")
	}
	allowed := []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"}
	var names []string
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(line, " ") {
			continue // a folded continuation
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("header line without a colon: %q", line)
		}
		names = append(names, name)
	}
	if !slices.Equal(names, allowed) {
		t.Errorf("headers = %v, want %v", names, allowed)
	}
	if body != "Body" {
		t.Errorf("body = %q, want %q", body, "Body")
	}
}

func TestBodyLineEndings(t *testing.T) {
	message := string(buildMessage("a@example.org", "a@example.org", Message{
		To:      "b@example.org",
		Subject: "s",
		Body:    "line one\n.hidden\nline three\r\nline four\rline five",
	}, time.Now()))
	if strings.Contains(strings.ReplaceAll(message, "\r\n", ""), "\n") || strings.Contains(strings.ReplaceAll(message, "\r\n", ""), "\r") {
		t.Error("a bare line ending survived into the message")
	}
	if !strings.Contains(message, "\r\n.hidden\r\n") {
		t.Errorf("a leading dot was changed before SMTP transport:\n%q", message)
	}
	if !strings.HasSuffix(message, "line four\r\nline five") {
		t.Error("the last lines were lost")
	}
}

// Dot-stuffing belongs to the transport alone. Doing it in the message as well
// would double the dots that arrive.
func TestSendPreservesLeadingDots(t *testing.T) {
	relay := newFakeRelay(t)
	body := ".first\n.\n..third\n.\r\nlast\n"
	if err := send(t, relay.config(TLSNone), Message{To: "b@example.org", Subject: "Dots", Body: body}); err != nil {
		t.Fatal(err)
	}
	// The relay decodes the SMTP dot protocol once, exactly as a real one
	// does, and textproto turns CRLF into LF.
	_, received, ok := strings.Cut(relay.delivered(t).data, "\n\n")
	if want := ".first\n.\n..third\n.\nlast\n"; !ok || received != want {
		t.Fatalf("received body = %q, want %q", received, want)
	}
}

func TestStartTLSRefusesUnencryptedRelay(t *testing.T) {
	relay := newFakeRelay(t, withAuth())
	cfg := relay.config(TLSStartTLS)
	cfg.Username, cfg.Password = "relay-user", "relay-pass"
	err := send(t, cfg, Message{To: "b@example.org", Subject: "Password reset", Body: resetSecret})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("STARTTLS mode delivered a reset secret over plaintext: %v", err)
	}
	got := relay.wait(t)
	if got.data != "" || got.from != "" {
		t.Fatal("message reached an unencrypted relay")
	}
	for _, command := range got.commands {
		if command == "AUTH" || command == "MAIL" {
			t.Fatalf("sent %s without encryption: %v", command, got.commands)
		}
	}
}

func TestStartTLSDeliversOverVerifiedTLS(t *testing.T) {
	cert, pool := testCertificate(t, "127.0.0.1")
	relay := newFakeRelay(t, withStartTLS(cert), withAuth())
	cfg := relay.config(TLSStartTLS)
	cfg.RootCAs = pool
	cfg.Username, cfg.Password = "relay-user", "relay-pass"
	if err := send(t, cfg, Message{To: "b@example.org", Subject: "Reset", Body: resetSecret}); err != nil {
		t.Fatal(err)
	}
	got := relay.delivered(t)
	want := []string{"EHLO", "STARTTLS", "tls:EHLO", "tls:AUTH", "tls:MAIL", "tls:RCPT", "tls:DATA", "tls:QUIT"}
	if !slices.Equal(got.commands, want) {
		t.Fatalf("commands = %v, want %v", got.commands, want)
	}
}

func TestTLSRefusesRelaysThatDoNotVerify(t *testing.T) {
	trusted, pool := testCertificate(t, "127.0.0.1")
	untrusted, _ := testCertificate(t, "127.0.0.1")
	wrongName, wrongPool := testCertificate(t, "smtp.example.org")
	for _, tc := range []struct {
		name string
		mode TLSMode
		cert *tls.Certificate
		pool *x509.CertPool
	}{
		{"starttls untrusted", TLSStartTLS, untrusted, pool},
		{"starttls system roots", TLSStartTLS, trusted, nil},
		{"starttls wrong name", TLSStartTLS, wrongName, wrongPool},
		{"implicit untrusted", TLSImplicit, untrusted, pool},
		{"implicit wrong name", TLSImplicit, wrongName, wrongPool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var relay *fakeRelay
			if tc.mode == TLSImplicit {
				relay = newImplicitRelay(t, tc.cert)
			} else {
				relay = newFakeRelay(t, withStartTLS(tc.cert))
			}
			cfg := relay.config(tc.mode)
			cfg.RootCAs = tc.pool
			cfg.Username, cfg.Password = "relay-user", "relay-pass"
			if err := send(t, cfg, Message{To: "b@example.org", Subject: "Reset", Body: resetSecret}); err == nil {
				t.Fatal("delivered to a relay whose certificate does not verify")
			}
			got := relay.wait(t)
			if got.data != "" || slices.Contains(got.commands, "tls:AUTH") || slices.Contains(got.commands, "AUTH") {
				t.Fatalf("credentials or mail reached the relay: %v", got.commands)
			}
		})
	}
}

func TestImplicitTLSDelivers(t *testing.T) {
	cert, pool := testCertificate(t, "127.0.0.1")
	relay := newImplicitRelay(t, cert)
	cfg := relay.config(TLSImplicit)
	cfg.RootCAs = pool
	if err := send(t, cfg, Message{To: "b@example.org", Subject: "Reset", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	got := relay.delivered(t)
	if !slices.Equal(got.commands, []string{"tls:EHLO", "tls:MAIL", "tls:RCPT", "tls:DATA", "tls:QUIT"}) {
		t.Fatalf("commands = %v", got.commands)
	}
}

// A relay that connects and then stalls must not hold a sender forever, at any
// step: the greeting, the TLS handshake, or after STARTTLS was accepted.
func TestSendBoundsStalledConnections(t *testing.T) {
	_, pool := testCertificate(t, "127.0.0.1")
	for _, tc := range []struct {
		stall string
		mode  TLSMode
	}{
		{"greeting", TLSNone}, {"greeting", TLSImplicit}, {"greeting", TLSStartTLS}, {"starttls", TLSStartTLS},
	} {
		for _, cancelAfterConnect := range []bool{false, true} {
			name := tc.stall + "/" + string(tc.mode) + "/timeout"
			if cancelAfterConnect {
				name = tc.stall + "/" + string(tc.mode) + "/cancel"
			}
			t.Run(name, func(t *testing.T) { checkStalledSend(t, tc.stall, tc.mode, cancelAfterConnect, pool) })
		}
	}
}

func checkStalledSend(t *testing.T, stall string, mode TLSMode, cancelAfterConnect bool, pool *x509.CertPool) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, listener)
	accepted := make(chan net.Conn, 1)
	go stallingRelay(listener, stall, accepted)
	timeout := 50 * time.Millisecond
	if cancelAfterConnect {
		timeout = time.Minute
	}
	sender, err := New(Config{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, From: "a@example.org", Mode: mode, Timeout: timeout, RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	// A longer caller deadline must not disable the sender's timeout.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sender.Send(ctx, Message{To: "b@example.org", Body: "secret"}) }()
	select {
	case conn := <-accepted:
		defer closeTest(t, conn)
	case <-time.After(time.Second):
		t.Fatal("sender did not connect")
	}
	if cancelAfterConnect {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled send succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send ignored its timeout or cancellation while waiting for the relay")
	}
}

// stallingRelay accepts one connection and then goes quiet, either before the
// greeting or right after agreeing to STARTTLS.
func stallingRelay(listener net.Listener, stall string, accepted chan<- net.Conn) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	accepted <- conn
	if stall != "starttls" {
		return
	}
	reader := bufio.NewReader(conn)
	_, _ = conn.Write([]byte("220 ready\r\n"))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "STARTTLS":
			_, _ = conn.Write([]byte("220 go ahead\r\n"))
			return // and never start the handshake
		default:
			_, _ = conn.Write([]byte("250-hi\r\n250 STARTTLS\r\n"))
		}
	}
}

func TestSubjectsAreEncodedForMail(t *testing.T) {
	decoder := new(mime.WordDecoder)
	for _, subject := range []string{
		"Choose a new password for Café Crew",
		"Neues Passwort für Bücherwurm — 日本語の掲示板",
		strings.Repeat("Ålesund ", 40),
		strings.Repeat("plain words ", 40),
		strings.Repeat("x", 200),
		"Plain ASCII subject",
		"=?utf-8?q?Looks_encoded?=",
		"Tabs\tand\x7fcontrols\x01",
		"emoji \U0001F511 key",
		"bad utf-8 \xff\xfe here",
		"",
		"a  b   c",
	} {
		raw := string(buildMessage(`"Witmoot" <no-reply@example.org>`, "no-reply@example.org", Message{To: "a@example.org", Subject: subject, Body: "x"}, time.Now()))
		head, _, _ := strings.Cut(raw, "\r\n\r\n")
		for _, line := range strings.Split(head, "\r\n") {
			if len(line) > 78 {
				t.Errorf("subject %q produced a %d-character header line", subject, len(line))
			}
			for i := 0; i < len(line); i++ {
				if line[i] > 126 || (line[i] < 32 && line[i] != '\t') {
					t.Fatalf("header carries raw byte %q: %q", line[i], line)
				}
			}
		}
		header := subjectHeader(t, raw)
		decoded, err := decoder.DecodeHeader(header)
		if err != nil {
			t.Fatalf("subject %q does not decode: %v", header, err)
		}
		if want := sanitiseHeader(subject); decoded != want {
			t.Fatalf("subject round trip = %q, want %q", decoded, want)
		}
	}
}

// subjectHeader returns the Subject value unfolded as RFC 5322 says: by
// removing each CRLF that is followed by white space, and nothing else.
func subjectHeader(t *testing.T, message string) string {
	t.Helper()
	head, _, _ := strings.Cut(message, "\r\n\r\n")
	unfolded := strings.ReplaceAll(head, "\r\n ", " ")
	for _, line := range strings.Split(unfolded, "\r\n") {
		if value, ok := strings.CutPrefix(line, "Subject: "); ok {
			return value
		}
	}
	t.Fatalf("no Subject header in %q", message)
	return ""
}

func TestMessagesCarryAUniqueMessageID(t *testing.T) {
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^<[0-9a-f]{32}@example\.org>$`)
	for range 50 {
		id := headersOf(t, string(buildMessage(`"Board" <no-reply@example.org>`, "no-reply@example.org", Message{To: "a@example.org", Subject: "s", Body: "b"}, time.Now()))).Get("Message-Id")
		if !pattern.MatchString(id) || seen[id] {
			t.Fatalf("message ID %q is malformed or repeated", id)
		}
		seen[id] = true
	}
	for envelope, want := range map[string]string{
		"no-reply@mail.example.org":             "mail.example.org",
		"no-reply@EXAMPLE.org":                  "EXAMPLE.org",
		"no-reply@localhost":                    "localhost",
		"":                                      "localhost",
		"no address at all":                     "localhost",
		"x@<evil>":                              "localhost",
		"x@[192.0.2.1]":                         "localhost",
		"x@exa mple.org":                        "localhost",
		"x@-bad.example.org":                    "localhost",
		"x@bad-.example.org":                    "localhost",
		"x@a..b":                                "localhost",
		"x@example.org.":                        "localhost",
		"x@exämple.org":                         "localhost",
		"y@z>\r\nBcc: x@example.org\r\n":        "localhost",
		"x@" + strings.Repeat("a", 64) + ".org": "localhost",
		"x@" + strings.Repeat(strings.Repeat("a", 60)+".", 5): "localhost",
		`"a@b"@example.org`: "example.org",
	} {
		id := messageID(envelope)
		if !strings.HasSuffix(id, "@"+want+">") || strings.ContainsAny(id, "\r\n \t") {
			t.Errorf("envelope %q produced message ID %q, want domain %s", envelope, id, want)
		}
	}
}

func TestParseFromCanonicalisesAndRejectsHostileSenders(t *testing.T) {
	for in, want := range map[string]string{
		"no-reply@example.org":             "<no-reply@example.org>",
		"Witmoot <no-reply@example.org>":   `"Witmoot" <no-reply@example.org>`,
		"imvault <no-reply@example.com>":   `"imvault" <no-reply@example.com>`,
		"Café Crew <no-reply@example.org>": "=?utf-8?q?Caf=C3=A9_Crew?= <no-reply@example.org>",
		" spaced@example.org ":             "<spaced@example.org>",
	} {
		if got, err := ParseFrom(in); err != nil || got != want {
			t.Errorf("ParseFrom(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "not an address", "Board <>", "a@b@c",
		"no-reply@example.org\r\nBcc: x@example.org", "Board <no-reply@example.org>\nX: y",
		"Board\r\n <no-reply@example.org>", "a@example.org, b@example.org", "<a@example.org", "a@example.org\x00",
		"0@0(\xff\\\\)", "Board\xff <a@example.org>",
	} {
		if got, err := ParseFrom(in); err == nil {
			t.Errorf("ParseFrom(%q) accepted %q", in, got)
		}
	}
}

func TestParseFromNeverAcceptsLineBreaks(t *testing.T) {
	property := func(prefix, suffix string, crlf uint8) bool {
		breaks := []string{"\r", "\n", "\r\n", "\n\r"}
		value := prefix + breaks[int(crlf)%len(breaks)] + suffix
		got, err := ParseFrom(value)
		return err != nil && got == ""
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 3000}); err != nil {
		t.Fatal(err)
	}
	// The same, around values that would otherwise be valid.
	for _, valid := range []string{"a@example.org", "Board <a@example.org>"} {
		for i := 0; i <= len(valid); i++ {
			for _, br := range []string{"\r", "\n", "\r\n", "\r\n "} {
				if got, err := ParseFrom(valid[:i] + br + valid[i:]); err == nil {
					t.Errorf("accepted %q as %q", valid[:i]+br+valid[i:], got)
				}
			}
		}
	}
}

func TestNewValidatesConfig(t *testing.T) {
	good := Config{Host: "smtp.example.org", Port: 587, From: "Board <no-reply@example.org>"}
	sender, err := New(good)
	if err != nil {
		t.Fatal(err)
	}
	if sender.cfg.Mode != TLSStartTLS || sender.timeout != DefaultTimeout {
		t.Fatalf("defaults: mode %q, timeout %s", sender.cfg.Mode, sender.timeout)
	}
	if sender.from != `"Board" <no-reply@example.org>` || sender.envelope != "no-reply@example.org" {
		t.Fatalf("sender %q, envelope %q", sender.from, sender.envelope)
	}
	for name, change := range map[string]func(*Config){
		"empty host":       func(c *Config) { c.Host = "" },
		"host with space":  func(c *Config) { c.Host = "smtp example.org" },
		"host with CRLF":   func(c *Config) { c.Host = "smtp.example.org\r\n" },
		"zero port":        func(c *Config) { c.Port = 0 },
		"negative port":    func(c *Config) { c.Port = -25 },
		"huge port":        func(c *Config) { c.Port = 65536 },
		"uppercase mode":   func(c *Config) { c.Mode = "STARTTLS" },
		"padded mode":      func(c *Config) { c.Mode = "starttls " },
		"unknown mode":     func(c *Config) { c.Mode = "tls" },
		"missing sender":   func(c *Config) { c.From = "" },
		"invalid sender":   func(c *Config) { c.From = "no-reply" },
		"injected sender":  func(c *Config) { c.From = "a@example.org\r\nBcc: b@example.org" },
		"several senders":  func(c *Config) { c.From = "a@example.org, b@example.org" },
		"negative timeout": func(c *Config) { c.Timeout = -time.Second },
	} {
		cfg := good
		change(&cfg)
		if sender, err := New(cfg); err == nil || sender != nil {
			t.Errorf("%s: New accepted %+v", name, cfg)
		}
	}
}

func TestParseTLSMode(t *testing.T) {
	for in, want := range map[string]TLSMode{"": TLSStartTLS, "starttls": TLSStartTLS, "implicit": TLSImplicit, "none": TLSNone} {
		if got, err := ParseTLSMode(in); err != nil || got != want {
			t.Errorf("ParseTLSMode(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"tls", "ssl", "STARTTLS", " none", "none\n", "off", "false"} {
		if got, err := ParseTLSMode(in); err == nil {
			t.Errorf("ParseTLSMode(%q) accepted %q", in, got)
		}
	}
}

// Imvault's durable queue wraps a Sender and is itself one; both senders here
// must fit that shape.
func TestSendersImplementSender(t *testing.T) {
	sender, err := New(Config{Host: "smtp.example.org", Port: 587, From: "a@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Sender{sender, Disabled{}, queue{next: sender}} {
		_ = s.Enabled()
	}
	if (queue{next: Disabled{}}).Enabled() {
		t.Fatal("a queue around a disabled sender claimed to be enabled")
	}
}

// queue is the shape of imvault's mail queue: a Sender that defers to another.
type queue struct{ next Sender }

func (q queue) Enabled() bool { return q.next.Enabled() }

func (q queue) Send(ctx context.Context, msg Message) error { return q.next.Send(ctx, msg) }

func FuzzParseFrom(f *testing.F) {
	for _, seed := range []string{"a@example.org", "Witmoot <no-reply@example.org>", "Café <a@b.c>", "a@b\r\nBcc: c@d", `"quoted\"name" <a@b>`, "=?utf-8?q?x?= <a@b>"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got, err := ParseFrom(value)
		if err != nil {
			return
		}
		if strings.ContainsAny(value, "\r\n") || strings.ContainsAny(got, "\r\n") {
			t.Fatalf("accepted a line break: %q => %q", value, got)
		}
		// Canonical output must itself be accepted, unchanged.
		again, err := ParseFrom(got)
		if err != nil || again != got {
			t.Fatalf("canonical form %q does not round trip: %q, %v", got, again, err)
		}
	})
}

func FuzzEncodeSubject(f *testing.F) {
	for _, seed := range []string{"", "Plain", "Café", "=?utf-8?q?x?=", strings.Repeat("Å", 100), "\xff\xfe", strings.Repeat("y", 100), "a  b"} {
		f.Add(seed)
	}
	decoder := new(mime.WordDecoder)
	f.Fuzz(func(t *testing.T, subject string) {
		subject = sanitiseHeader(subject)
		encoded := encodeSubject(subject)
		for i, line := range strings.Split(encoded, "\r\n") {
			if i > 0 && !strings.HasPrefix(line, " ") {
				t.Fatalf("a line break is not a fold: %q", encoded)
			}
			if strings.ContainsAny(line, "\r\n") {
				t.Fatalf("bare line ending in %q", encoded)
			}
			for j := 0; j < len(line); j++ {
				if line[j] < 0x20 || line[j] > 0x7e {
					t.Fatalf("raw byte %q in %q", line[j], encoded)
				}
			}
			if len(line) > 78 {
				t.Fatalf("%d-character line in %q", len(line), encoded)
			}
		}
		decoded, err := decoder.DecodeHeader(strings.ReplaceAll(encoded, "\r\n", ""))
		if err != nil || decoded != subject {
			t.Fatalf("%q encoded as %q decodes to %q, %v", subject, encoded, decoded, err)
		}
	})
}
