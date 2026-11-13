// SPDX-License-Identifier: AGPL-3.0-or-later

// Package problem writes plain-language error responses: an HTML page inside
// the app's layout, or JSON for API requests. Internal details go to the log,
// never to the response.
package problem

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Page is what the app's layout shows for a problem.
type Page struct {
	Status  int
	Title   string
	Message string
}

// Default titles and messages, by status.
var defaults = map[int]Page{
	http.StatusBadRequest:            {Title: "That didn't work", Message: "Something in that request didn't look right. Go back and try again."},
	http.StatusForbidden:             {Title: "Not available", Message: "Your account can't open this page."},
	http.StatusNotFound:              {Title: "Nothing here", Message: "This page doesn't exist, or it isn't shared with you."},
	http.StatusRequestEntityTooLarge: {Title: "Too large", Message: "That was too large to save. Try something smaller."},
	http.StatusUnprocessableEntity:   {Title: "That didn't work", Message: "Some of that couldn't be saved. Check it and try again."},
	http.StatusTooManyRequests:       {Title: "Take a short break", Message: "Too many attempts. Wait a few minutes and try again."},
	http.StatusInternalServerError:   {Title: "Something went wrong", Message: "Something went wrong on our side. Please try again."},
}

// ExpiredMessage is shown when a form's CSRF token has expired.
const ExpiredMessage = "This page was open for a while. Reload it and try again."

// maxDetail caps the logged detail, in bytes.
const maxDetail = 512

// For returns the default page for status. Unlisted 4xx statuses use the 400
// text and everything else the 500 text, keeping status.
func For(status int) Page {
	page, ok := defaults[status]
	if !ok {
		page = defaults[http.StatusInternalServerError]
		if status >= 400 && status < 500 {
			page = defaults[http.StatusBadRequest]
		}
	}
	page.Status = status
	return page
}

// Writer writes problem responses. The zero value writes a minimal
// standalone page and logs with slog.Default.
type Writer struct {
	// Layout renders page inside the app's layout, with the status already
	// chosen. If it is nil or returns an error, a minimal page is written.
	Layout func(w http.ResponseWriter, r *http.Request, page Page) error
	// Logger receives details; nil means slog.Default.
	Logger *slog.Logger
}

// Write sends a problem response for status. message replaces the default
// text when it is not empty; keep it plain and free of internal detail.
// detail, if not nil, is logged (capped at 512 bytes) and never sent.
func (pw Writer) Write(w http.ResponseWriter, r *http.Request, status int, message string, detail error) {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	page := For(status)
	if message != "" {
		page.Message = message
	}
	pw.log(r, status, detail)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if WantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(struct {
			Status int    `json:"status"`
			Error  string `json:"error"`
		}{status, page.Message})
		return
	}
	if pw.Layout != nil && pw.writeLayout(w, r, page) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = fallback.Execute(w, page)
}

// writeLayout renders the app's layout into a buffer and sends it, or
// reports false so the caller writes the minimal page.
func (pw Writer) writeLayout(w http.ResponseWriter, r *http.Request, page Page) bool {
	buffer := &bufferedWriter{header: http.Header{}}
	if err := pw.Layout(buffer, r, page); err != nil {
		pw.logger().ErrorContext(r.Context(), "render problem page", "error", capped(err.Error()))
		return false
	}
	for key, values := range buffer.header {
		w.Header()[key] = values
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(page.Status)
	_, _ = w.Write(buffer.body.Bytes())
	return true
}

// Expired answers a form whose CSRF token has expired with 403 and
// ExpiredMessage. For htmx requests it also sets HX-Refresh so the page
// reloads with a fresh token.
func (pw Writer) Expired(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
	}
	pw.Write(w, r, http.StatusForbidden, ExpiredMessage, nil)
}

// WantsJSON reports whether r asks for JSON rather than HTML: its Accept
// header names application/json and not text/html, or its path is under
// /api/.
func WantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}

func (pw Writer) logger() *slog.Logger {
	if pw.Logger != nil {
		return pw.Logger
	}
	return slog.Default()
}

func (pw Writer) log(r *http.Request, status int, detail error) {
	if detail == nil {
		return
	}
	level := slog.LevelInfo
	if status >= 500 {
		level = slog.LevelError
	}
	pw.logger().Log(r.Context(), level, "request problem", "status", status, "method", r.Method, "route", capped(r.Pattern), "error", capped(detail.Error()))
}

// capped shortens text to maxDetail bytes on a rune boundary.
func capped(text string) string {
	if len(text) <= maxDetail {
		return text
	}
	cut := maxDetail
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

var fallback = template.Must(template.New("problem").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>{{.Title}}</title></head><body><main class="comfy-auth comfy-problem"><h1>{{.Title}}</h1><p>{{.Message}}</p><p><a href="/">Go to the home page</a></p></main></body></html>`))
