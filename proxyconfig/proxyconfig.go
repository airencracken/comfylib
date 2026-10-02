// SPDX-License-Identifier: AGPL-3.0-or-later

// Package proxyconfig prints a reverse-proxy site configuration for an app,
// generated from the example files the app ships in its packages.
//
// Each app keeps its own examples and passes them in as an fs.FS, usually an
// embed.FS, because go:embed cannot reach across modules. Every value that is
// substituted into an example is validated first, so a hostname or path given
// on the command line can never add a directive to the output.
package proxyconfig

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
)

// serverList names the supported proxies, in the order they are recommended.
// It is a constant rather than a slice so that no package state can change.
const serverList = "caddy|nginx|apache"

// Spec describes one app's examples and settings.
type Spec struct {
	// App is the command name, such as "witmoot". It appears in usage text
	// and in the service configuration paths printed in the header.
	App string
	// EnvPrefix starts the app's setting names, such as "WITMOOT", giving
	// WITMOOT_ADDR and WITMOOT_BASE_URL. Empty means App in upper case.
	EnvPrefix string
	// ExampleDomain and DefaultUpstream are the hostname and loopback
	// address written in the examples; generated output replaces both.
	ExampleDomain   string
	DefaultUpstream string
	// ProxyEnvironment is the setting line that makes the app trust the
	// proxy, such as "WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128".
	ProxyEnvironment string
	// Examples holds the example files.
	Examples fs.FS
	// Paths maps each server to its example in Examples. When nil, the
	// defaults are caddy/Caddyfile, nginx/APP.conf and apache/APP.conf.
	Paths map[string]string
}

// Options selects and fills in one configuration.
type Options struct {
	// Server is caddy, nginx or apache.
	Server string
	// Domain is the public hostname.
	Domain string
	// Upstream is the app's loopback host:port; Spec.DefaultUpstream when
	// empty.
	Upstream string
	// Certificate and Key are for nginx and Apache. They default to the
	// Let's Encrypt paths for Domain.
	Certificate, Key string
}

// Run implements "APP proxy-config SERVER --domain HOST [OPTIONS]", printing
// one configuration to out. It never opens the database, writes system files,
// obtains certificates, or reloads a running proxy. args excludes the
// "proxy-config" word itself.
func Run(spec Spec, args []string, out io.Writer) error {
	if err := spec.check(); err != nil {
		return err
	}
	app := spec.App
	if len(args) == 0 {
		return fmt.Errorf("usage: %s proxy-config %s --domain HOST [OPTIONS]", app, serverList)
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintf(out, `Usage: %s proxy-config %s --domain HOST [OPTIONS]

Print a complete HTTPS site configuration to stdout. Caddy is recommended and
manages certificates automatically. nginx and Apache need existing certificates
and renewal; their default paths use /etc/letsencrypt/live/HOST/.
The upstream must be a loopback host:port on the same machine.

Example:
  %s proxy-config caddy --domain %s > %s.Caddyfile

Use "%s proxy-config nginx --help" for options. The generated comments include
the application settings to put in your service configuration.
`, app, serverList, app, spec.ExampleDomain, app, app)
		return err
	}
	options := Options{Server: args[0]}
	if _, err := spec.templatePath(options.Server); err != nil {
		return err
	}
	flags := flag.NewFlagSet(app+" proxy-config "+options.Server, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&options.Domain, "domain", "", "public hostname, without a scheme or path (required)")
	flags.StringVar(&options.Upstream, "upstream", spec.DefaultUpstream, "loopback application host:port; match the service's listen address")
	flags.StringVar(&options.Certificate, "tls-cert", "", "nginx/Apache certificate path (default: /etc/letsencrypt/live/HOST/fullchain.pem)")
	flags.StringVar(&options.Key, "tls-key", "", "nginx/Apache key path (default: /etc/letsencrypt/live/HOST/privkey.pem)")
	flags.Usage = func() {
		// A flag set's Usage cannot report a failed write; PrintDefaults
		// below drops write errors in the same way.
		_, _ = fmt.Fprintf(out, "Usage: %s proxy-config %s --domain HOST [OPTIONS]\n\nPrint an HTTPS site config to stdout; no system files are changed.\n\n", app, options.Server)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments; use proxy-config --help")
	}
	config, err := Render(spec, options)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, config)
	return err
}

// Render validates every substituted value before generating any output.
func Render(spec Spec, options Options) (string, error) {
	if err := spec.check(); err != nil {
		return "", err
	}
	path, err := spec.templatePath(options.Server)
	if err != nil {
		return "", err
	}
	if !validDomain(options.Domain) {
		return "", errors.New("--domain must be a DNS hostname without a scheme, port, wildcard, or path")
	}
	if options.Upstream == "" {
		options.Upstream = spec.DefaultUpstream
	}
	if !validUpstream(options.Upstream) {
		return "", fmt.Errorf("--upstream must be localhost or a loopback IP with a port from 1 to 65535 (for example %s)", spec.DefaultUpstream)
	}
	if options.Server == "caddy" && (options.Certificate != "" || options.Key != "") {
		return "", errors.New("--tls-cert and --tls-key apply to nginx and Apache; Caddy manages TLS automatically")
	}
	if (options.Certificate == "") != (options.Key == "") {
		return "", errors.New("provide both --tls-cert and --tls-key")
	}
	if options.Certificate == "" {
		options.Certificate = "/etc/letsencrypt/live/" + options.Domain + "/fullchain.pem"
		options.Key = "/etc/letsencrypt/live/" + options.Domain + "/privkey.pem"
	}
	if !validCertificatePath(options.Certificate) || !validCertificatePath(options.Key) {
		return "", errors.New("TLS paths must be absolute and use only letters, digits, spaces, /, ., _, and -")
	}
	data, err := fs.ReadFile(spec.Examples, path)
	if err != nil {
		return "", fmt.Errorf("proxyconfig: read example: %w", err)
	}
	config := strings.NewReplacer(
		"/etc/letsencrypt/live/"+spec.ExampleDomain+"/fullchain.pem", strconv.Quote(options.Certificate),
		"/etc/letsencrypt/live/"+spec.ExampleDomain+"/privkey.pem", strconv.Quote(options.Key),
		spec.ExampleDomain, options.Domain,
		spec.DefaultUpstream, options.Upstream,
	).Replace(string(data))
	app, prefix := spec.App, spec.envPrefix()
	header := fmt.Sprintf("# Generated by %s proxy-config %s.\n# Set these in /etc/conf.d/%s (OpenRC) or /etc/%s/%s.env (systemd):\n# %s_ADDR=%s\n# %s_BASE_URL=https://%s\n# %s_SECURE_COOKIES=true\n# %s\n# Validate the complete proxy configuration before reloading.\n\n",
		app, options.Server, app, app, app, prefix, options.Upstream, prefix, options.Domain, prefix, spec.ProxyEnvironment)
	return header + config, nil
}

// check catches a Spec that could never render correctly. These are mistakes
// in the app, not in what a person typed.
func (spec Spec) check() error {
	switch {
	case spec.App == "" || !validName(spec.App):
		return fmt.Errorf("proxyconfig: invalid app name %q", spec.App)
	case spec.EnvPrefix != "" && !validName(spec.EnvPrefix):
		return fmt.Errorf("proxyconfig: invalid setting prefix %q", spec.EnvPrefix)
	case !validDomain(spec.ExampleDomain):
		return fmt.Errorf("proxyconfig: invalid example domain %q", spec.ExampleDomain)
	case !validUpstream(spec.DefaultUpstream):
		return fmt.Errorf("proxyconfig: invalid default upstream %q", spec.DefaultUpstream)
	case strings.ContainsAny(spec.ProxyEnvironment, "\r\n"):
		return errors.New("proxyconfig: the proxy setting must be one line")
	case spec.Examples == nil:
		return errors.New("proxyconfig: no example files")
	}
	return nil
}

func (spec Spec) envPrefix() string {
	if spec.EnvPrefix != "" {
		return spec.EnvPrefix
	}
	return strings.ToUpper(spec.App)
}

func (spec Spec) templatePath(server string) (string, error) {
	for _, known := range strings.Split(serverList, "|") {
		if server != known {
			continue
		}
		path, ok := spec.Paths[server]
		switch {
		case spec.Paths != nil && ok:
			return path, nil
		case spec.Paths != nil:
			// An app that names its paths provides only those servers.
		case server == "caddy":
			return "caddy/Caddyfile", nil
		default:
			return server + "/" + spec.App + ".conf", nil
		}
	}
	return "", fmt.Errorf("unsupported proxy %q; choose caddy, nginx, or apache", server)
}

func validName(name string) bool {
	for _, ch := range name {
		if !asciiAlphanumeric(ch) && ch != '-' && ch != '_' {
			return false
		}
	}
	return name != ""
}

func validDomain(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !asciiAlphanumeric(ch) && ch != '-' {
				return false
			}
		}
	}
	return true
}

func validUpstream(upstream string) bool {
	host, port, err := net.SplitHostPort(upstream)
	if err != nil || strings.Trim(port, "0123456789") != "" {
		return false
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return false
	}
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.Zone() == "" && address.IsLoopback()
}

func validCertificatePath(path string) bool {
	if !filepath.IsAbs(path) || strings.HasSuffix(path, "/") {
		return false
	}
	for _, ch := range path {
		if !asciiAlphanumeric(ch) && !strings.ContainsRune("/._- ", ch) {
			return false
		}
	}
	return true
}

func asciiAlphanumeric(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}
