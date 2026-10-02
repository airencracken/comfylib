// SPDX-License-Identifier: AGPL-3.0-or-later

// Package smtp sends the small number of transactional messages the apps need,
// such as password reset links, through an SMTP relay.
//
// Sending is optional. An instance with no relay keeps a Disabled sender that
// reports Enabled() == false and refuses every message, so the app can fall
// back to links an administrator hands over. Nothing in this package logs a
// message body: a reset link in a log is as good as the password it replaces.
//
// A configured relay is held to the protection it was configured with. In the
// default STARTTLS mode a relay that does not offer STARTTLS, or presents a
// certificate that does not verify, gets nothing: not the credentials and not
// the message. Every step, from connecting to the final reply, is bounded by
// the configured timeout and by the caller's context.
package smtp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Message is one plain-text email.
type Message struct {
	// To is a single bare address such as "alice@example.org".
	To      string
	Subject string
	Body    string
}

// Sender delivers messages, or declines to. Imvault's durable queue is one,
// wrapping an *SMTP.
type Sender interface {
	// Enabled reports whether messages will actually leave the process.
	Enabled() bool
	Send(ctx context.Context, msg Message) error
}

// TLSMode selects how the connection to the relay is protected.
type TLSMode string

const (
	// TLSStartTLS upgrades an ordinary connection, which is what most relays
	// on port 587 expect. It is the default, and it is required: a relay
	// that does not offer it is refused.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit encrypts from the first byte, as port 465 expects.
	TLSImplicit TLSMode = "implicit"
	// TLSNone is for a trusted local relay or a development catcher.
	TLSNone TLSMode = "none"
)

// ParseTLSMode reads a mode as written in a setting. Empty means STARTTLS.
// The error lists the accepted values; callers add the setting's name.
func ParseTLSMode(value string) (TLSMode, error) {
	switch mode := TLSMode(value); mode {
	case "":
		return TLSStartTLS, nil
	case TLSStartTLS, TLSImplicit, TLSNone:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown TLS mode %q; use starttls, implicit, or none", value)
	}
}

// DefaultTimeout bounds one Send when Config.Timeout is not set.
const DefaultTimeout = 15 * time.Second

// Config describes an SMTP relay.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope and header sender, for example
	// "Witmoot <no-reply@example.org>". It must pass ParseFrom.
	From string
	// Mode defaults to STARTTLS when empty.
	Mode TLSMode
	// Timeout bounds one whole Send; DefaultTimeout when zero.
	Timeout time.Duration
	// RootCAs verifies the relay's certificate; the system pool when nil.
	// It is there for relays with a private CA, and for tests.
	RootCAs *x509.CertPool
}

// SMTP sends mail through a relay.
type SMTP struct {
	cfg      Config
	from     string // canonical header form
	envelope string // bare address for MAIL FROM
	timeout  time.Duration
}

// New checks cfg and returns a sender for the relay it describes. An unknown
// mode is an error rather than a quiet fall back to plain text.
func New(cfg Config) (*SMTP, error) {
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, " \t\r\n/") {
		return nil, fmt.Errorf("smtp: invalid relay host %q", cfg.Host)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp: relay port %d is outside 1 to 65535", cfg.Port)
	}
	mode, err := ParseTLSMode(string(cfg.Mode))
	if err != nil {
		return nil, fmt.Errorf("smtp: %w", err)
	}
	cfg.Mode = mode
	address, err := parseFrom(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("smtp: sender: %w", err)
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("smtp: negative timeout")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &SMTP{cfg: cfg, from: address.String(), envelope: address.Address, timeout: timeout}, nil
}

// Enabled always reports true: a configured sender is expected to work, and
// failures are surfaced to the caller instead of being silently swallowed.
func (s *SMTP) Enabled() bool { return true }

// ErrInvalidMessage reports a message that cannot be sent as given, such as a
// recipient or subject carrying a line break that would start a new header.
var ErrInvalidMessage = errors.New("smtp: invalid message")

// Send delivers one message.
func (s *SMTP) Send(ctx context.Context, msg Message) (err error) {
	if err := checkMessage(msg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	// Closing the dialled connection also closes any TLS layer the client
	// adds. After a successful Quit it is already closed, which is expected.
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("smtp: close: %w", closeErr))
		}
	}()
	// Cover the greeting as well as the SMTP exchange. Cancelling a context
	// without a deadline must also interrupt an already connected relay.
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: set deadline: %w", err)
	}
	// Closing interrupts a stalled exchange, which then reports the failure;
	// the close itself has nothing further to say.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	client, err := netsmtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	if err := s.secure(client); err != nil {
		return err
	}
	if err := s.deliver(client, msg); err != nil {
		return err
	}
	return client.Quit()
}

// secure upgrades the session when STARTTLS is required and authenticates.
func (s *SMTP) secure(client *netsmtp.Client) error {
	if s.cfg.Mode == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp: relay does not support required STARTTLS")
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth refuses to hand over credentials on an unencrypted
		// connection unless the relay is on the loopback interface, which is
		// exactly the check we want.
		auth := netsmtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	return nil
}

func (s *SMTP) deliver(client *netsmtp.Client, msg Message) error {
	if err := client.Mail(s.envelope); err != nil {
		return fmt.Errorf("smtp: sender rejected: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: recipient rejected: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: start body: %w", err)
	}
	// The DATA writer applies SMTP dot-stuffing; the message itself must
	// not, or a line starting with a dot would arrive changed.
	if _, err := writer.Write(buildMessage(s.from, s.envelope, msg, time.Now())); err != nil {
		return errors.Join(fmt.Errorf("smtp: write body: %w", err), writer.Close())
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp: finish body: %w", err)
	}
	return nil
}

func (s *SMTP) dial(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := (&net.Dialer{Timeout: s.timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("smtp: dial %s: %w", address, err)
	}
	if s.cfg.Mode != TLSImplicit {
		return conn, nil
	}
	tlsConn := tls.Client(conn, s.tlsConfig())
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("smtp: handshake: %w", err), conn.Close())
	}
	return tlsConn, nil
}

func (s *SMTP) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName: s.cfg.Host,
		MinVersion: tls.VersionTLS12,
		RootCAs:    s.cfg.RootCAs,
	}
}

// ErrDisabled is returned by a Sender for an instance without a relay.
var ErrDisabled = errors.New("mail is not configured")

// Disabled is a Sender for instances with no relay configured. It neither
// sends nor records anything.
type Disabled struct{}

// Enabled reports false.
func (Disabled) Enabled() bool { return false }

// Send refuses the message without logging any part of it.
func (Disabled) Send(context.Context, Message) error { return ErrDisabled }

// ParseFrom validates a From value such as "Witmoot <no-reply@example.org>"
// and returns it in canonical form, with a non-ASCII display name encoded.
func ParseFrom(value string) (string, error) {
	address, err := parseFrom(value)
	if err != nil {
		return "", err
	}
	return address.String(), nil
}

func parseFrom(value string) (*mail.Address, error) {
	// net/mail rejects line breaks today; refusing them here as well keeps
	// that guarantee independent of its parser.
	if strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("the sender must be on one line")
	}
	if !utf8.ValidString(value) {
		return nil, errors.New("the sender is not valid UTF-8")
	}
	address, err := mail.ParseAddress(value)
	if err != nil {
		return nil, fmt.Errorf("the sender is not a valid address: %w", err)
	}
	// The canonical form is what goes into the header, so it has to parse
	// back to the same sender. Odd input can parse once and yet render as
	// something a reader would reject or read differently.
	again, err := mail.ParseAddress(address.String())
	if err != nil || *again != *address {
		return nil, errors.New("the sender cannot be written as a valid header")
	}
	return address, nil
}

// checkMessage refuses values that would change the message's structure. A
// recipient or subject with a line break could add headers or recipients.
func checkMessage(msg Message) error {
	if msg.To == "" {
		return fmt.Errorf("%w: no recipient", ErrInvalidMessage)
	}
	for _, r := range msg.To {
		if r < 0x21 || r == 0x7f || r == '<' || r == '>' || r == ',' {
			return fmt.Errorf("%w: the recipient must be one bare address", ErrInvalidMessage)
		}
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return fmt.Errorf("%w: the subject must be on one line", ErrInvalidMessage)
	}
	return nil
}

// buildMessage renders an RFC 5322 plain-text message.
//
// checkMessage has already refused line breaks, but header values are
// stripped of them again here, so no path into this function can add a header.
func buildMessage(from, envelope string, msg Message, now time.Time) []byte {
	var buf bytes.Buffer
	writeHeader(&buf, "From", sanitiseHeader(from))
	writeHeader(&buf, "To", sanitiseHeader(msg.To))
	writeHeader(&buf, "Subject", encodeSubject(sanitiseHeader(msg.Subject)))
	writeHeader(&buf, "Date", now.Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", messageID(envelope))
	writeHeader(&buf, "MIME-Version", "1.0")
	writeHeader(&buf, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&buf, "Content-Transfer-Encoding", "8bit")
	buf.WriteString("\r\n")
	buf.WriteString(normaliseBody(msg.Body))
	return buf.Bytes()
}

// messageID is a unique identifier in the sender's domain. Many relays and spam
// filters distrust a message without one. A domain that is not a plain DNS name
// is replaced by localhost rather than copied into the header.
func messageID(envelope string) string {
	domain := "localhost"
	if at := strings.LastIndexByte(envelope, '@'); at >= 0 && validDomain(envelope[at+1:]) {
		domain = envelope[at+1:]
	}
	random := make([]byte, 16)
	// crypto/rand.Read terminates the process if the system RNG fails.
	_, _ = rand.Read(random)
	return "<" + hex.EncodeToString(random) + "@" + domain + ">"
}

func validDomain(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !letter && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func writeHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteString(name)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

// sanitiseHeader removes anything that could terminate the header or start a
// new one.
func sanitiseHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// normaliseBody applies CRLF line endings. The SMTP DATA writer handles dot
// stuffing on the wire; doing it here too would change the delivered body.
func normaliseBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}
