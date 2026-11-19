// SPDX-License-Identifier: AGPL-3.0-or-later

package twofactor

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/comfylib/password"
	"github.com/airencracken/comfylib/ratelimit"
	"github.com/airencracken/comfylib/token"
	"github.com/airencracken/comfylib/totp"
)

// LoginPath is the public route for the second sign-in step.
const LoginPath = "/login/2fa"

// AccountPath is the signed-in account settings route.
const AccountPath = "/account/two-factor"

// Hooks attach the shared forms to the host's cookies, CSRF middleware and
// layout. Request supplies the signed-in ID, raw session and form CSRF token
// after the host has authenticated the request and validated POST bodies.
type Hooks struct {
	Request       func(*http.Request) (user int64, session, csrf string)
	CookieName    func(*http.Request) string
	Session       func(http.ResponseWriter, *http.Request, string)
	CheckPassword func(hash, plain string) bool
	Render        func(http.ResponseWriter, *http.Request, int, string, template.HTML)
	Fail          func(http.ResponseWriter, *http.Request, error)
}

// HTTP handles optional authenticator enrollment and the second sign-in step.
type HTTP struct {
	store        *store
	hooks        Hooks
	issuer, home string
	limits       *ratelimit.Window
	now          func() time.Time
}

// New attaches the factor store. An empty key leaves the option disabled on
// an instance without enrolled accounts. A restored database with enrolled
// accounts requires the same instance key.
func New(ctx context.Context, db *sql.DB, key, issuer, home string, sessions Sessions, hooks Hooks) (*HTTP, error) {
	s, err := open(ctx, db, key, sessions)
	if err != nil {
		return nil, err
	}
	if hooks.Request == nil || hooks.CookieName == nil || hooks.Session == nil || hooks.CheckPassword == nil ||
		hooks.Render == nil || hooks.Fail == nil || !strings.HasPrefix(home, "/") || strings.HasPrefix(home, "//") {
		return nil, errors.New("twofactor: missing or invalid request hooks")
	}
	return &HTTP{store: s, hooks: hooks, issuer: issuer, home: home, now: time.Now,
		limits: ratelimit.NewWindow(ratelimit.WindowConfig{Attempts: 8, Interval: time.Minute, Capacity: 4096, RejectNew: true})}, nil
}

// Enabled reports whether the instance offers the setup option.
func (h *HTTP) Enabled() bool { return h != nil && h.store.cipher != nil }

// Register adds the routes when the instance offers authenticator enrollment.
func (h *HTTP) Register(mux *http.ServeMux) {
	if !h.Enabled() {
		return
	}
	mux.HandleFunc("GET "+LoginPath, h.prompt)
	mux.HandleFunc("POST "+LoginPath, h.login)
	mux.HandleFunc("GET "+AccountPath, h.account)
	mux.HandleFunc("POST "+AccountPath, h.update)
}

// Start finishes a verified first factor. A nonempty verifiedHash is the
// password hash the caller verified. Supply an empty hash only after a
// verified identity-provider callback or redemption of a reset token.
func (h *HTTP) Start(w http.ResponseWriter, r *http.Request, id int64, verifiedHash string) (target string, handled bool, err error) {
	if h == nil {
		return "", false, nil
	}
	_, _, browser := h.hooks.Request(r)
	result, err := h.store.firstFactor(r.Context(), id, verifiedHash, browser, h.now())
	if err != nil {
		return "", true, err
	}
	if result.session != "" {
		h.hooks.Session(w, r, result.session)
		h.cookie(w, r, "", -1)
		return h.home, true, nil
	}
	h.cookie(w, r, result.challenge, int(challengeTTL.Seconds()))
	return LoginPath, true, nil
}

func (h *HTTP) cookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	name := h.hooks.CookieName(r)
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, // #nosec G124 -- The host selects its TLS-aware cookie name. HTTP loopback operates without Secure.
		HttpOnly: true, Secure: strings.HasPrefix(name, "__Host-"), SameSite: http.SameSiteLaxMode})
}

func (h *HTTP) pending(r *http.Request) string {
	c, err := r.Cookie(h.hooks.CookieName(r))
	if err != nil {
		return ""
	}
	return c.Value
}

func (h *HTTP) expired(w http.ResponseWriter, r *http.Request) {
	h.cookie(w, r, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *HTTP) prompt(w http.ResponseWriter, r *http.Request) {
	_, _, csrf := h.hooks.Request(r)
	err := h.store.prompt(r.Context(), h.pending(r), csrf, h.now())
	switch {
	case errors.Is(err, errExpired):
		h.expired(w, r)
	case err != nil:
		h.hooks.Fail(w, r, err)
	default:
		h.render(w, r, http.StatusOK, view{Prompt: true, CSRF: csrf})
	}
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	_, _, csrf := h.hooks.Request(r)
	if !validForm(r, csrf) {
		http.Error(w, "Invalid form", http.StatusForbidden)
		return
	}
	raw, err := h.store.secondFactor(r.Context(), h.pending(r), csrf, r.PostForm.Get("code"), h.now())
	switch {
	case errors.Is(err, errExpired) || errors.Is(err, errPassword):
		h.expired(w, r)
	case errors.Is(err, errInvalid):
		h.render(w, r, http.StatusUnauthorized, view{Prompt: true, CSRF: csrf, Error: "That code did not work. Try a fresh authenticator code or an unused recovery code."})
	case errors.Is(err, errRate):
		w.Header().Set("Retry-After", "900")
		h.render(w, r, http.StatusTooManyRequests, view{Prompt: true, CSRF: csrf, Error: "Too many attempts. Wait 15 minutes, then start sign-in again."})
	case err != nil:
		h.hooks.Fail(w, r, err)
	default:
		h.hooks.Session(w, r, raw)
		h.cookie(w, r, "", -1)
		http.Redirect(w, r, h.home, http.StatusSeeOther)
	}
}

func validForm(r *http.Request, csrf string) bool {
	if csrf == "" || len(r.PostForm["csrf"]) != 1 || !token.Equal(r.PostForm.Get("csrf"), csrf) {
		return false
	}
	for _, values := range r.PostForm {
		if len(values) != 1 {
			return false
		}
	}
	return true
}

func (h *HTTP) account(w http.ResponseWriter, r *http.Request) {
	h.accountView(w, r, http.StatusOK, "", nil)
}

func (h *HTTP) accountView(w http.ResponseWriter, r *http.Request, status int, message string, codes []string) {
	id, session, csrf := h.hooks.Request(r)
	if id == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	account, err := h.store.account(r.Context(), id, session, h.now())
	if err != nil {
		h.hooks.Fail(w, r, err)
		return
	}
	data := view{CSRF: csrf, Enabled: account.enabled, Count: account.codes, Codes: codes, Error: message}
	if account.secret != "" {
		data.Secret = totp.GroupSpaced(account.secret)
		data.URI = template.URL(totp.ProvisioningURI(h.issuer, "account-"+strconv.FormatInt(id, 10), account.secret)) // #nosec G203 -- ProvisioningURI fixes the scheme and escapes its input.
	}
	h.render(w, r, status, data)
}

func (h *HTTP) update(w http.ResponseWriter, r *http.Request) {
	id, session, csrf := h.hooks.Request(r)
	if id == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !validForm(r, csrf) {
		http.Error(w, "Invalid form", http.StatusForbidden)
		return
	}
	limit := strconv.FormatInt(id, 10)
	if ok, _ := h.limits.Allow(limit); !ok {
		w.Header().Set("Retry-After", "60")
		h.accountView(w, r, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.", nil)
		return
	}
	codes, err := h.updateAccount(r, id, session)
	switch {
	case errors.Is(err, errPassword):
		h.accountView(w, r, http.StatusForbidden, "Check your password or sign in again.", nil)
	case errors.Is(err, errInvalid):
		h.accountView(w, r, http.StatusUnprocessableEntity, "That code did not work. Use a fresh code from your authenticator.", nil)
	case errors.Is(err, errExpired):
		h.accountView(w, r, http.StatusConflict, "That setup is no longer open. Start it again if needed.", nil)
	case err != nil:
		h.hooks.Fail(w, r, err)
	case len(codes) > 0:
		h.limits.Refund(limit)
		h.accountView(w, r, http.StatusOK, "", codes)
	default:
		h.limits.Refund(limit)
		http.Redirect(w, r, AccountPath, http.StatusSeeOther)
	}
}

func (h *HTTP) updateAccount(r *http.Request, id int64, session string) ([]string, error) {
	action := r.PostForm.Get("action")
	code := r.PostForm.Get("code")
	ctx, now := r.Context(), h.now()
	if action == "enable" {
		return h.store.enable(ctx, id, session, code, now)
	}
	if action != "start" && action != "renew" && action != "disable" {
		return nil, errExpired
	}
	hash, err := h.store.credential(ctx, h.store.db, id)
	if err != nil {
		return nil, err
	}
	plain := r.PostForm.Get("password")
	if !password.Fits(plain) || !h.hooks.CheckPassword(hash, plain) {
		return nil, errPassword
	}
	if action == "start" {
		return nil, h.store.begin(ctx, id, session, hash, now)
	}
	return h.store.change(ctx, id, session, hash, code, action == "disable", now)
}

type view struct {
	Prompt, Enabled     bool
	CSRF, Error, Secret string
	URI                 template.URL
	Count               int
	Codes               []string
}

func (h *HTTP) render(w http.ResponseWriter, r *http.Request, status int, data view) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	var content bytes.Buffer
	if err := forms.Execute(&content, data); err != nil {
		h.hooks.Fail(w, r, err)
		return
	}
	title := "Two-factor sign-in"
	if data.Prompt {
		title = "Enter your sign-in code"
	}
	h.hooks.Render(w, r, status, title, template.HTML(content.String())) // #nosec G203 -- All interpolated values have already passed through html/template.
}
