// SPDX-License-Identifier: AGPL-3.0-or-later
package problem

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

var layout = template.Must(template.New("layout").Parse(`<header class="topbar">App</header><main class="panel problem"><h1>{{.Title}}</h1><p>{{.Message}}</p></main>`))

func appWriter(logs *bytes.Buffer) Writer {
	return Writer{
		Layout: func(w http.ResponseWriter, _ *http.Request, page Page) error { return layout.Execute(w, page) },
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	}
}

func TestEveryStatusHasPlainCopyInTheLayout(t *testing.T) {
	for _, status := range []int{400, 403, 404, 413, 422, 429, 500} {
		var logs bytes.Buffer
		w := httptest.NewRecorder()
		appWriter(&logs).Write(w, httptest.NewRequest(http.MethodGet, "/x", nil), status, "", nil)
		body := w.Body.String()
		page := For(status)
		if w.Code != status || !strings.Contains(body, `<header class="topbar">`) || !strings.Contains(body, template.HTMLEscapeString(page.Message)) || page.Title == "" {
			t.Fatal(status, w.Code, body)
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatal("headers", w.Header())
		}
		if logs.Len() != 0 {
			t.Fatal("logged without detail", logs.String())
		}
	}
	if For(418).Message != For(400).Message || For(418).Status != 418 || For(503).Message != For(500).Message {
		t.Fatal("unlisted statuses")
	}
}

func TestDetailIsLoggedCappedAndNeverSent(t *testing.T) {
	var logs bytes.Buffer
	secret := "sql: no such table users; APP_SECRET=" + strings.Repeat("é", 600)
	w := httptest.NewRecorder()
	appWriter(&logs).Write(w, httptest.NewRequest(http.MethodPost, "/save", nil), 500, "", errors.New(secret))
	if strings.Contains(w.Body.String(), "sql:") || strings.Contains(w.Body.String(), "APP_SECRET") {
		t.Fatal("detail reached the response")
	}
	line := logs.String()
	if !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "sql: no such table") || strings.Count(line, "é") > maxDetail/2 || !utf8.ValidString(line) {
		t.Fatal("log", line)
	}
	logs.Reset()
	appWriter(&logs).Write(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), 404, "", errors.New("missing row"))
	if !strings.Contains(logs.String(), "level=INFO") {
		t.Fatal("client errors log at info", logs.String())
	}
}

func TestAPIRequestsGetJSON(t *testing.T) {
	for _, r := range []*http.Request{httptest.NewRequest(http.MethodGet, "/api/items", nil), httptest.NewRequest(http.MethodGet, "/items", nil)} {
		if r.URL.Path == "/items" {
			r.Header.Set("Accept", "application/json")
		}
		w := httptest.NewRecorder()
		appWriter(&bytes.Buffer{}).Write(w, r, 429, "", errors.New("bucket empty"))
		var body struct {
			Status int    `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status != 429 || body.Error != For(429).Message || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal(w.Body.String(), err)
		}
	}
	browser := httptest.NewRequest(http.MethodGet, "/items", nil)
	browser.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9")
	if WantsJSON(browser) {
		t.Fatal("browser treated as API client")
	}
}

func TestExpiredFormsRefreshUnderHtmx(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/save", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	appWriter(&bytes.Buffer{}).Expired(w, r)
	if w.Code != 403 || w.Header().Get("HX-Refresh") != "true" || !strings.Contains(w.Body.String(), template.HTMLEscapeString(ExpiredMessage)) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	w = httptest.NewRecorder()
	appWriter(&bytes.Buffer{}).Expired(w, httptest.NewRequest(http.MethodPost, "/save", nil))
	if w.Header().Get("HX-Refresh") != "" {
		t.Fatal("plain form asked to refresh")
	}
}

func TestLayoutFailureFallsBackToAMinimalPage(t *testing.T) {
	var logs bytes.Buffer
	pw := Writer{Layout: func(w http.ResponseWriter, _ *http.Request, _ Page) error {
		_, _ = w.Write([]byte("half a page"))
		return errors.New("template broke")
	}, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	w := httptest.NewRecorder()
	pw.Write(w, httptest.NewRequest(http.MethodGet, "/", nil), 404, `<script>alert(1)</script>`, nil)
	body := w.Body.String()
	if w.Code != 404 || strings.Contains(body, "half a page") || strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(logs.String(), "template broke") {
		t.Fatal(body, logs.String())
	}
	w = httptest.NewRecorder()
	Writer{}.Write(w, httptest.NewRequest(http.MethodGet, "/", nil), 403, "", nil)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "comfy-problem") {
		t.Fatal("zero Writer", w.Body.String())
	}
}

func TestOutOfRangeStatusesBecomeServerErrors(t *testing.T) {
	for _, status := range []int{0, -1, 200, 302, 600, 999, 1 << 30} {
		w := httptest.NewRecorder()
		Writer{}.Write(w, httptest.NewRequest(http.MethodGet, "/", nil), status, "", nil)
		if w.Code != 500 {
			t.Fatal(status, w.Code)
		}
	}
}

func TestLayoutHeadersAndStatusAreKept(t *testing.T) {
	pw := Writer{Layout: func(w http.ResponseWriter, _ *http.Request, page Page) error {
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.WriteHeader(http.StatusOK) // ignored: the problem status wins
		_, err := w.Write([]byte(page.Title))
		return err
	}}
	w := httptest.NewRecorder()
	pw.Write(w, httptest.NewRequest(http.MethodGet, "/", nil), 413, "", nil)
	if w.Code != 413 || w.Header().Get("Content-Security-Policy") == "" || w.Body.String() != For(413).Title {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}

func FuzzCapped(f *testing.F) {
	f.Add(strings.Repeat("é", 400))
	f.Fuzz(func(t *testing.T, text string) {
		got := capped(text)
		if len(got) > maxDetail+3 || (utf8.ValidString(text) && !utf8.ValidString(got)) {
			t.Fatal(len(got))
		}
	})
}
