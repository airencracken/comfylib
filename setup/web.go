// SPDX-License-Identifier: AGPL-3.0-or-later

package setup

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/airencracken/comfylib/password"
	"github.com/airencracken/comfylib/token"
)

//go:embed page.html
var pageHTML string

//go:embed style.css
var style string
var pageTemplate = template.Must(template.New("setup").Parse(pageHTML))

// Wrap holds a fresh instance at /setup until its first owner exists. Health
// checks remain available; initialized applications keep their normal handler.
func (h *Handler) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ready, err := h.ready(r.Context())
		if err != nil {
			http.Error(w, "Could not check setup status", http.StatusServiceUnavailable)
			return
		}
		if ready {
			if r.URL.Path == "/setup" || strings.HasPrefix(r.URL.Path, "/setup/") {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ServeHTTP serves only setup routes. Use Wrap to guard an application's routes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.URL.Path == "/setup/style.css" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(style))
		}
		return
	}
	if r.URL.Path != "/setup" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
		} else {
			http.Error(w, "Create the first owner through setup", http.StatusServiceUnavailable)
		}
		return
	}
	http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.form)).ServeHTTP(w, r)
}
func (h *Handler) cookieName(r *http.Request) string {
	if h.secure(r) {
		return "__Host-" + h.config.App + "-setup"
	}
	return h.config.App + "-setup"
}
func (h *Handler) secure(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(h.config.BaseURL, "https://")
}
func (h *Handler) form(w http.ResponseWriter, r *http.Request) {
	ready, err := h.ready(r.Context())
	if err != nil {
		http.Error(w, "Could not check setup status", http.StatusServiceUnavailable)
		return
	}
	if ready {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session := ""
	if c, e := r.Cookie(h.cookieName(r)); e == nil && len(c.Value) == 64 {
		session = c.Value
	}
	if session == "" {
		session, _ = token.New()
		// #nosec G124 -- Secure follows HTTPS; loopback HTTP setup is also supported.
		http.SetCookie(w, &http.Cookie{Name: h.cookieName(r), Value: session, Path: "/", MaxAge: 3600, HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteStrictMode})
	}
	csrf := token.SessionCSRF(session, "comfyware:setup")
	if r.Method != http.MethodPost {
		h.render(w, csrf, "", http.StatusOK)
		return
	}
	h.submit(w, r, csrf)
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request, csrf string) {
	if !h.allowedTransport(r) {
		h.render(w, csrf, "Use HTTPS, or a browser on the host’s loopback address, to finish setup.", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		h.render(w, csrf, "This form is too large or could not be read.", http.StatusBadRequest)
		return
	}
	if len(r.PostForm["csrf"]) != 1 || !token.Equal(r.PostForm.Get("csrf"), csrf) {
		h.render(w, csrf, "Reload this page before trying again.", http.StatusForbidden)
		return
	}
	key, err := h.loadKey()
	if err != nil || len(r.PostForm["key"]) != 1 || !token.Equal(r.PostForm.Get("key"), hex.EncodeToString(key)) {
		h.render(w, csrf, "The setup key is incorrect. Run the setup command on your host to see it.", http.StatusForbidden)
		return
	}
	owner := Owner{Username: strings.TrimSpace(r.PostForm.Get("username")), Password: r.PostForm.Get("password")}
	if owner.Password != r.PostForm.Get("confirm") {
		h.render(w, csrf, "Passwords do not match.", http.StatusBadRequest)
		return
	}
	if err = password.Validate(owner.Password); err != nil {
		h.render(w, csrf, err.Error(), http.StatusBadRequest)
		return
	}
	if err = h.config.Create(r.Context(), owner); err != nil {
		if errors.Is(err, ErrComplete) {
			h.complete.Store(true)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		h.render(w, csrf, "Could not create the account. Check the username and try again.", http.StatusBadRequest)
		return
	}
	h.complete.Store(true)
	if err = os.Remove(h.keyPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not remove completed setup key", "app", h.config.App, "error", err)
	}
	// #nosec G124 -- Secure follows HTTPS; loopback HTTP setup is also supported.
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login?setup=complete", http.StatusSeeOther)
}
func (h *Handler) render(w http.ResponseWriter, csrf, message string, status int) {
	var b bytes.Buffer
	data := struct{ App, CSRF, Error string }{h.config.App, csrf, message}
	if err := pageTemplate.Execute(&b, data); err != nil {
		http.Error(w, "Could not show setup", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func (h *Handler) allowedTransport(r *http.Request) bool {
	if h.secure(r) {
		return true
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
