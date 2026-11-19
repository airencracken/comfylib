// SPDX-License-Identifier: AGPL-3.0-or-later

// Package factortest exercises the two-factor flow through a host application's
// real router without starting a listening server.
package factortest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/totp"
	"github.com/airencracken/comfylib/twofactor"
)

// Config identifies the instance and a new account eligible to enroll.
type Config struct {
	Handler  http.Handler
	Origin   string
	Username string
	Password string
	Home     string
}

type browser struct {
	t       *testing.T
	config  Config
	cookies map[string]*http.Cookie
	csrf    string
	address string
}

var (
	csrfField    = regexp.MustCompile(`name="csrf" value="([a-f0-9]{64})"`)
	setupKey     = regexp.MustCompile(`id="authenticator-key">([^<]+)`)
	recoveryCode = regexp.MustCompile(`<li><code>([A-Z2-9]{5}-[A-Z2-9]{5})</code></li>`)
)

func newBrowser(t *testing.T, cfg Config, address string) *browser {
	b := &browser{t: t, config: cfg, address: address, cookies: make(map[string]*http.Cookie)}
	b.get("/login", 200)
	return b
}

func (b *browser) request(method, path string, form url.Values, status int) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest(method, b.config.Origin+path, strings.NewReader(form.Encode()))
	r.RemoteAddr = b.address + ":1234"
	for _, cookie := range b.cookies {
		r.AddCookie(cookie)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	b.config.Handler.ServeHTTP(w, r)
	if w.Code != status {
		b.t.Fatalf("%s %s returned %d, want %d", method, path, w.Code, status)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	if match := csrfField.FindStringSubmatch(w.Body.String()); len(match) > 0 {
		b.csrf = match[1]
	}
	return w
}

func (b *browser) get(path string, status int) *httptest.ResponseRecorder {
	return b.request("GET", path, nil, status)
}

func (b *browser) post(path string, form url.Values, status int) *httptest.ResponseRecorder {
	b.t.Helper()
	form.Set("csrf", b.csrf)
	return b.request("POST", path, form, status)
}

func (b *browser) redirect(w *httptest.ResponseRecorder, target string) {
	b.t.Helper()
	if value := w.Header().Get("Location"); value != target {
		b.t.Fatalf("redirected to %q, want %q", value, target)
	}
}

func (b *browser) firstFactor(target string) {
	b.t.Helper()
	b.get("/login", 200)
	b.redirect(b.post("/login", url.Values{"username": {b.config.Username}, "password": {b.config.Password}}, 303), target)
}

func (b *browser) begin() string {
	b.t.Helper()
	page := b.get("/account", 200)
	if !strings.Contains(page.Body.String(), `href="/account/two-factor"`) {
		b.t.Fatal("account lacks its two-factor settings link")
	}
	b.get(twofactor.AccountPath, 200)
	b.post(twofactor.AccountPath, url.Values{"action": {"start"}, "password": {"wrong password"}}, 403)
	b.redirect(b.post(twofactor.AccountPath, url.Values{"action": {"start"}, "password": {b.config.Password}}, 303), twofactor.AccountPath)
	draft := b.get(twofactor.AccountPath, 200)
	match := setupKey.FindStringSubmatch(draft.Body.String())
	if len(match) != 2 || draft.Header().Get("Cache-Control") != "no-store" {
		b.t.Fatal("authenticator setup key missing or cacheable")
	}
	return match[1]
}

func (b *browser) enable(secret string) (string, []string) {
	b.t.Helper()
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		b.t.Fatal(err)
	}
	done := b.post(twofactor.AccountPath, url.Values{"action": {"enable"}, "code": {code}}, 200)
	return code, readCodes(b.t, done.Body.String())
}

func readCodes(t *testing.T, body string) []string {
	t.Helper()
	var codes []string
	seen := make(map[string]bool)
	for _, match := range recoveryCode.FindAllStringSubmatch(body, -1) {
		if seen[match[1]] {
			t.Fatal("duplicate recovery code")
		}
		seen[match[1]] = true
		codes = append(codes, match[1])
	}
	if len(codes) != totp.RecoveryCodeCount {
		t.Fatalf("got %d recovery codes", len(codes))
	}
	return codes
}

// Run verifies enrollment, revoked sessions, browser binding, one-time codes,
// recovery, rotation and disabling against the application's middleware.
func Run(t *testing.T, cfg Config) {
	t.Helper()
	primary := newBrowser(t, cfg, "192.0.2.42")
	secondary := newBrowser(t, cfg, "192.0.2.43")
	primary.firstFactor(cfg.Home)
	secondary.firstFactor(cfg.Home)
	secret := primary.begin()
	if w := secondary.get(twofactor.AccountPath, 200); setupKey.MatchString(w.Body.String()) {
		t.Fatal("enrollment key visible to another session")
	}
	used, codes := primary.enable(secret)
	secondary.get("/account", 303)
	if page := primary.get(twofactor.AccountPath, 200); recoveryCode.MatchString(page.Body.String()) || setupKey.MatchString(page.Body.String()) {
		t.Fatal("enrollment secrets appeared again")
	}
	guest := newBrowser(t, cfg, "192.0.2.44")
	guest.firstFactor(twofactor.LoginPath)
	guest.get("/account", 303)
	guest.get(twofactor.LoginPath, 200)
	rejectOtherBrowser(t, cfg, guest)
	guest.request("POST", twofactor.LoginPath, url.Values{"csrf": {"wrong"}, "code": {codes[0]}}, 403)
	guest.post(twofactor.LoginPath, url.Values{"code": {used}}, 401)
	guest.redirect(guest.post(twofactor.LoginPath, url.Values{"code": {codes[0]}}, 303), cfg.Home)
	guest.get("/account", 200)
	recovered := newBrowser(t, cfg, "192.0.2.45")
	recovered.firstFactor(twofactor.LoginPath)
	recovered.post(twofactor.LoginPath, url.Values{"code": {codes[0]}}, 401)
	recovered.redirect(recovered.post(twofactor.LoginPath, url.Values{"code": {codes[1]}}, 303), cfg.Home)
	recovered.get(twofactor.AccountPath, 200)
	replacements := recovered.post(twofactor.AccountPath, url.Values{"action": {"renew"}, "password": {cfg.Password}, "code": {codes[2]}}, 200)
	fresh := readCodes(t, replacements.Body.String())
	recovered.post(twofactor.AccountPath, url.Values{"action": {"disable"}, "password": {cfg.Password}, "code": {codes[3]}}, 422)
	recovered.redirect(recovered.post(twofactor.AccountPath, url.Values{"action": {"disable"}, "password": {cfg.Password}, "code": {fresh[0]}}, 303), twofactor.AccountPath)
	recovered.get(twofactor.AccountPath, 200)
	newBrowser(t, cfg, "192.0.2.46").firstFactor(cfg.Home)
}

func rejectOtherBrowser(t *testing.T, cfg Config, valid *browser) {
	t.Helper()
	other := newBrowser(t, cfg, "192.0.2.47")
	for name, cookie := range valid.cookies {
		if strings.HasSuffix(name, "twofactor") {
			if !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("pending sign-in cookie has weak attributes")
			}
			other.cookies[name] = cookie
			other.redirect(other.get(twofactor.LoginPath, 303), "/login")
			return
		}
	}
	t.Fatalf("missing pending sign-in cookie for %s", cfg.Origin)
}
