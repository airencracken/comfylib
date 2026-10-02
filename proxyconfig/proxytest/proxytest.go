// SPDX-License-Identifier: AGPL-3.0-or-later

// Package proxytest runs an app's generated nginx and Apache configurations in
// real servers and checks what reaches the app through them.
//
// It starts each server unprivileged on loopback ports with a throwaway
// certificate, in front of a stand-in backend, and checks the redirect to
// HTTPS, request bodies larger than nginx's default limit, header replacement
// (a client's own X-Forwarded-For and friends must not survive), range
// requests, error statuses and streaming.
//
// Call it from a test behind a build tag, as both apps do with
// proxyintegration, so that ordinary test runs never need the servers. Once
// the tag is given, a missing server is a failure rather than a skip, so a CI
// job cannot pass without testing anything. NGINX_BINARY, APACHE_BINARY and
// APACHE_MODULE_DIR override where the servers and Apache's modules are found.
package proxytest

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/airencracken/comfylib/proxyconfig"
)

// echo is what the stand-in backend reports about each request it receives.
type echo struct {
	Host, URI, Method, Digest string
	Headers                   http.Header
}

// ReverseProxies checks spec's nginx and Apache configurations, each in a
// subtest of its own.
func ReverseProxies(t *testing.T, spec proxyconfig.Spec) {
	t.Helper()
	for _, kind := range []string{"nginx", "apache"} {
		t.Run(kind, func(t *testing.T) { checkProxy(t, spec, kind) })
	}
}

func checkProxy(t *testing.T, spec proxyconfig.Spec, kind string) {
	stream := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBackend(w, r, stream)
	}))
	defer backend.Close()
	defer close(stream)
	plain, secure, roots := startProxy(t, spec, kind, backend.URL)
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	t.Run("redirect", func(t *testing.T) {
		resp := request(t, client, http.MethodGet, plain+"/photo%20name?q=a%2Bb", nil)
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://localhost/photo%20name?q=a%2Bb" {
			t.Fatalf("redirect: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
		}
	})
	t.Run("headers-and-upload", func(t *testing.T) { checkHeadersAndUpload(t, client, secure) })
	t.Run("range-and-access", func(t *testing.T) { checkRangeAndAccess(t, client, secure) })
	t.Run("streaming", func(t *testing.T) {
		resp := request(t, client, http.MethodGet, secure+"/stream", nil)
		defer closeBody(t, resp)
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil || line != "first\n" {
			t.Fatalf("stream held until completion: %q %v", line, err)
		}
	})
}

// serveBackend stands in for the app. Errors writing a response cannot be
// reported from here; the client side of each check sees them.
func serveBackend(w http.ResponseWriter, r *http.Request, stream <-chan struct{}) {
	switch r.URL.Path {
	case "/range":
		http.ServeContent(w, r, "clip.mp4", time.Time{}, strings.NewReader("0123456789"))
	case "/stream":
		_, _ = fmt.Fprintln(w, "first")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-stream:
		case <-r.Context().Done():
		}
	case "/private":
		http.Error(w, "permission denied", http.StatusForbidden)
	default:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "test", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/"})
		_ = json.NewEncoder(w).Encode(echo{r.Host, r.RequestURI, r.Method, fmt.Sprintf("%x", sha256.Sum256(body)), r.Header})
	}
}

func checkHeadersAndUpload(t *testing.T, client *http.Client, secure string) {
	// Exceeds nginx's default 1 MiB limit; also exercises chunked uploads.
	body := bytes.Repeat([]byte("upload data\n"), 200000)
	req, err := http.NewRequest(http.MethodPost, secure+"/photo%20name?q=a%2Bb", struct{ io.Reader }{bytes.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	req.Header.Add("X-Forwarded-For", "192.0.2.4")
	req.Header.Set("X-Real-IP", "192.0.2.4")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "spoof.example")
	req.Header.Set("Forwarded", "for=192.0.2.4;proto=http")
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Cookie", "session=test")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", secure)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status=%d", resp.StatusCode)
	}
	var got echo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Host != strings.TrimPrefix(secure, "https://") || got.URI != "/photo%20name?q=a%2Bb" || got.Method != http.MethodPost || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
		t.Fatalf("request changed in transit: %+v", got)
	}
	for key, want := range map[string]string{
		"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1",
		"X-Forwarded-Proto": "https", "X-Forwarded-Host": got.Host, "Forwarded": "",
		"Authorization": "Bearer test-key", "Cookie": "session=test", "HX-Request": "true", "Origin": secure,
	} {
		if value := got.Headers.Get(key); value != want {
			t.Errorf("%s=%q; want %q", key, value, want)
		}
	}
	if values := got.Headers.Values("X-Forwarded-For"); len(values) != 1 {
		t.Errorf("X-Forwarded-For arrived as %q; the client's values must be replaced", values)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("cookie changed: %v", cookies)
	}
}

func checkRangeAndAccess(t *testing.T, client *http.Client, secure string) {
	req, err := http.NewRequest(http.MethodGet, secure+"/range", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-5")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	closeBody(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusPartialContent || string(body) != "2345" || resp.Header.Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	resp = request(t, client, http.MethodGet, secure+"/private", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("private status=%d", resp.StatusCode)
	}
}

func request(t *testing.T, client *http.Client, method, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// certificate writes a self-signed certificate for localhost into dir and
// returns a pool that trusts it, so the client verifies the proxy for real.
func certificate(t *testing.T, dir string) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "cert.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "key.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return pool
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// serverBinary finds nginx or apache2, preferring NGINX_BINARY or
// APACHE_BINARY.
func serverBinary(t *testing.T, kind string) string {
	t.Helper()
	if binary := os.Getenv(strings.ToUpper(kind) + "_BINARY"); binary != "" {
		return binary
	}
	name := kind
	if kind == "apache" {
		name = "apache2"
	}
	binary, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("install %s or set %s_BINARY: %v", name, strings.ToUpper(kind), err)
	}
	return binary
}

// startProxy renders and starts one server, returning its plain and TLS base
// URLs and the roots that verify it. The server is stopped when the test ends.
func startProxy(t *testing.T, spec proxyconfig.Spec, kind, backend string) (string, string, *x509.CertPool) {
	t.Helper()
	dir := t.TempDir()
	// A space in the path proves certificate paths are quoted.
	certDir := filepath.Join(dir, "TLS certs")
	if err := os.Mkdir(certDir, 0o700); err != nil {
		t.Fatal(err)
	}
	roots := certificate(t, certDir)
	plain, secure := freePort(t), freePort(t)
	data, err := proxyconfig.Render(spec, proxyconfig.Options{
		Server: kind, Domain: "localhost", Upstream: strings.TrimPrefix(backend, "http://"),
		Certificate: filepath.Join(certDir, "cert.pem"), Key: filepath.Join(certDir, "key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer(
		"listen [::]:80;", "", "listen [::]:443 ssl;", "",
		"listen 80;", "listen 127.0.0.1:"+plain+";", "listen 443 ssl;", "listen 127.0.0.1:"+secure+" ssl;",
		"*:80", "127.0.0.1:"+plain, "*:443", "127.0.0.1:"+secure,
	).Replace(data)
	binary := serverBinary(t, kind)
	path := filepath.Join(dir, "proxy.conf")
	args := []string{"-f", path, "-DFOREGROUND"}
	check := []string{"-f", path, "-t"}
	if kind == "nginx" {
		// Every temporary path is set, because nginx otherwise tries to
		// create its packaged defaults, which an unprivileged user cannot.
		temp := ""
		for _, kind := range []string{"client_body", "proxy", "fastcgi", "uwsgi", "scgi"} {
			temp += fmt.Sprintf("%s_temp_path %s;\n", kind, filepath.Join(dir, kind))
		}
		config = fmt.Sprintf("daemon off;\nmaster_process off;\npid %s/pid;\nerror_log stderr;\nevents {}\nhttp {\naccess_log off;\n%s%s\n}\n", dir, temp, config)
		args = []string{"-p", dir, "-c", path}
		check = append(append([]string{}, args...), "-t")
	} else {
		config = apachePreamble(t, binary, dir, plain, secure) + config
	}
	write(t, path, config)
	if output, err := exec.Command(binary, check...).CombinedOutput(); err != nil {
		t.Fatalf("config validation: %v\n%s", err, output)
	}
	runServer(t, dir, binary, args)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+secure, 100*time.Millisecond)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			return "http://localhost:" + plain, "https://localhost:" + secure, roots
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy did not start")
	return "", "", nil
}

// runServer starts the server in the foreground and arranges to stop it, and
// to print its logs if the test failed.
func runServer(t *testing.T, dir, binary string, args []string) {
	t.Helper()
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err, log.Close())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		// A server that already exited cannot be signalled; Wait below
		// reports how it ended.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill() // it may have exited after all
			<-done
		}
		if err := log.Close(); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			for _, name := range []string{"process.log", "error.log"} {
				if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
					t.Logf("%s:\n%s", name, data)
				}
			}
		}
	})
}

func apachePreamble(t *testing.T, binary, dir, plain, secure string) string {
	t.Helper()
	modules := os.Getenv("APACHE_MODULE_DIR")
	if modules == "" {
		modules = "/usr/lib/apache2/modules"
	}
	compiled, err := exec.Command(binary, "-l").CombinedOutput()
	if err != nil {
		t.Fatalf("apache modules: %v %s", err, compiled)
	}
	var config strings.Builder
	fmt.Fprintf(&config, "ServerRoot %q\nDefaultRuntimeDir %q\nPidFile %q\nErrorLog %q\nServerName localhost\nListen 127.0.0.1:%s\nListen 127.0.0.1:%s\n", dir, dir, filepath.Join(dir, "pid"), filepath.Join(dir, "error.log"), plain, secure)
	for _, module := range []string{"mpm_event", "unixd", "authz_core", "alias", "ssl", "socache_shmcb", "headers", "proxy", "proxy_http"} {
		if !strings.Contains(string(compiled), "mod_"+module+".c") && !strings.Contains(string(compiled), module+".c") {
			fmt.Fprintf(&config, "LoadModule %s_module %q\n", module, filepath.Join(modules, "mod_"+module+".so"))
		}
	}
	fmt.Fprintf(&config, "User #%d\nGroup #%d\n", os.Getuid(), os.Getgid())
	return config.String()
}
