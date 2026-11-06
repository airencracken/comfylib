// SPDX-License-Identifier: AGPL-3.0-or-later

package setup

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/airencracken/comfylib/token"
)

type fixture struct {
	mu    sync.Mutex
	owner bool
	calls int
	h     *Handler
	key   string
	fail  bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := new(fixture)
	var err error
	f.h, err = New(Config{App: "testapp", Directory: filepath.Join(t.TempDir(), "data"), Address: "127.0.0.1:8080", Ready: func(context.Context) (bool, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.owner, nil }, Create: func(_ context.Context, owner Owner) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.owner {
			return ErrComplete
		}
		if f.fail {
			return errors.New("injected failure")
		}
		if owner.Username != "alex" {
			return errors.New("invalid username")
		}
		f.owner = true
		f.calls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = f.h.Enable(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	key, err := f.h.loadKey()
	if err != nil {
		t.Fatal(err)
	}
	f.key = hex.EncodeToString(key)
	if !strings.Contains(out.String(), "http://127.0.0.1:8080/setup") || !strings.Contains(out.String(), f.key) {
		t.Fatal("missing setup instructions")
	}
	return f
}
func (f *fixture) request(method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "testapp-setup", Value: strings.Repeat("a", 64), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w := httptest.NewRecorder()
	f.h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
	return w
}
func (f *fixture) form() url.Values {
	return url.Values{"csrf": {token.SessionCSRF(strings.Repeat("a", 64), "comfyware:setup")}, "key": {f.key}, "username": {"alex"}, "password": {"a long private password"}, "confirm": {"a long private password"}}
}
func TestSetupFlowRequiresHostKeyAndCloses(t *testing.T) {
	f := newFixture(t)
	w := f.request(http.MethodGet, "/login", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/setup" {
		t.Fatal(w.Code)
	}
	w = f.request(http.MethodGet, "/setup", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Create owner account") || strings.Contains(w.Body.String(), f.key) {
		t.Fatal("unsafe setup page", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("setup may be cached")
	}
	if w = f.request(http.MethodGet, "/healthz", nil); w.Code != http.StatusNoContent {
		t.Fatal("health check blocked")
	}
	if w = f.request(http.MethodPost, "/join", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatal("pre-setup signup allowed")
	}
	if w = f.request(http.MethodGet, "/setup/style.css", nil); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Fatal("shared stylesheet missing")
	}
	form := f.form()
	form.Set("key", "wrong")
	if w = f.request(http.MethodPost, "/setup", form); w.Code != http.StatusForbidden {
		t.Fatal("host key guard bypassed")
	}
	form = f.form()
	form.Set("csrf", "wrong")
	if w = f.request(http.MethodPost, "/setup", form); w.Code != http.StatusForbidden {
		t.Fatal("CSRF guard bypassed")
	}
	if w = f.request(http.MethodPost, "/setup", f.form()); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?setup=complete" {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := os.Stat(f.h.keyPath()); !os.IsNotExist(err) {
		t.Fatal("used key retained", err)
	}
	if err := f.h.Enable(t.Context(), &bytes.Buffer{}); !errors.Is(err, ErrComplete) {
		t.Fatal("initialized setup reopened", err)
	}
	if w = f.request(http.MethodPost, "/setup", f.form()); w.Code != http.StatusSeeOther || f.calls != 1 {
		t.Fatal("setup replay created another owner")
	}
	if w = f.request(http.MethodGet, "/", nil); w.Code != http.StatusNoContent {
		t.Fatal("initialized app blocked")
	}
}
func TestSetupInputFailureAndConcurrency(t *testing.T) {
	f := newFixture(t)
	for _, field := range []string{"key", "csrf", "confirm", "password", "username"} {
		form := f.form()
		form.Set(field, "")
		if w := f.request(http.MethodPost, "/setup", form); w.Code < http.StatusBadRequest || f.calls != 0 {
			t.Fatal("invalid setup mutated account", field, w.Code)
		}
	}
	f.fail = true
	if w := f.request(http.MethodPost, "/setup", f.form()); w.Code != http.StatusBadRequest {
		t.Fatal("callback failure ignored")
	}
	if _, err := f.h.loadKey(); err != nil {
		t.Fatal("failed setup consumed key")
	}
	f.fail = false
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { f.request(http.MethodPost, "/setup", f.form()) })
	}
	wg.Wait()
	if f.calls != 1 {
		t.Fatal("multiple first owners created", f.calls)
	}
}
func TestSetupKeyPermissionsAndAtomicCreation(t *testing.T) {
	f := newFixture(t)
	original := f.key
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := f.h.Enable(context.Background(), &bytes.Buffer{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	key, err := f.h.loadKey()
	if err != nil || hex.EncodeToString(key) != original {
		t.Fatal("concurrent enables disagree", err)
	}
	// #nosec G302 -- Deliberately public fixture must be rejected.
	if err = os.Chmod(f.h.keyPath(), 0644); err != nil {
		t.Fatal(err)
	}
	if err = f.h.Enable(t.Context(), &bytes.Buffer{}); err == nil {
		t.Fatal("public setup key accepted")
	}
	if w := f.request(http.MethodPost, "/setup", f.form()); w.Code != http.StatusForbidden {
		t.Fatal("public key used")
	}
	if err := os.Remove(f.h.keyPath()); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte(original+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, f.h.keyPath()); err != nil {
		t.Fatal(err)
	}
	if err = f.h.Enable(t.Context(), &bytes.Buffer{}); err == nil {
		t.Fatal("symlink key accepted")
	}
}
func TestSetupOriginMethodsAndBodyLimits(t *testing.T) {
	f := newFixture(t)
	if w := f.request(http.MethodDelete, "/setup", nil); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	form := f.form()
	form.Set("extra", strings.Repeat("x", 9000))
	if w := f.request(http.MethodPost, "/setup", form); w.Code != http.StatusBadRequest {
		t.Fatal("oversized form accepted")
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/setup", strings.NewReader(f.form().Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(&http.Cookie{Name: "testapp-setup", Value: strings.Repeat("a", 64), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-origin setup allowed", w.Code)
	}
}

func TestSetupTransportAndWorkerWait(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest(http.MethodPost, "http://public.example/setup", strings.NewReader(f.form().Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "testapp-setup", Value: strings.Repeat("a", 64), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("public HTTP setup allowed", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.h.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("worker ignored cancellation", err)
	}
	if w = f.request(http.MethodPost, "/setup", f.form()); w.Code != http.StatusSeeOther {
		t.Fatal(w.Code)
	}
	if err := f.h.Wait(t.Context()); err != nil {
		t.Fatal("initialized worker blocked", err)
	}
}

func TestInitializedInstanceNeedsNoSetupFiles(t *testing.T) {
	f := newFixture(t)
	f.mu.Lock()
	f.owner = true
	f.mu.Unlock()
	fresh, err := New(f.h.config)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	fresh.Wrap(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/setup", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatal("existing owner did not close setup", w.Code)
	}
	if err = fresh.Enable(t.Context(), &bytes.Buffer{}); !errors.Is(err, ErrComplete) {
		t.Fatal("existing instance got a setup key", err)
	}
}
