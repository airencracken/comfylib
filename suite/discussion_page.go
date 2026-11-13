// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"html/template"
	"net/http"
)

var discussionPage = template.Must(template.New("return").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>Keep discussion link</title><link rel="stylesheet" href="{{.Stylesheet}}"></head><body><main class="comfy-auth"><h1>Keep this discussion link?</h1><p>The discussion has its own audience in Witmoot. Saving the link does not change who can see this item.</p><p><a href="{{.Thread}}" rel="noreferrer">Open discussion in Witmoot</a></p><form method="post" action="{{.Action}}"><input type="hidden" name="{{.CSRFField}}" value="{{.CSRFToken}}"><input type="hidden" name="url" value="{{.Thread}}"><input type="hidden" name="discussion_state" value="{{.State}}"><button type="submit">Keep discussion link</button></form><p><a href="{{.Source}}">Go back without saving</a></p></main></body></html>`))

// DiscussionPage describes the page that asks a member to keep a returned
// discussion link. The form posts the fields "url" and "discussion_state"
// plus the CSRF field to Action.
type DiscussionPage struct {
	Action     string // the app's authenticated POST route that saves the link
	Source     string // the item the discussion is about
	Thread     string // the Witmoot topic URL, already checked
	State      string // the pending return token
	CSRFField  string // the form field name the app reads its CSRF token from
	CSRFToken  string
	Stylesheet string // URL of the shared components.css
}

// RenderDiscussionPage writes the confirmation page with Cache-Control:
// no-store. Values are HTML-escaped.
func RenderDiscussionPage(w http.ResponseWriter, page DiscussionPage) error {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return discussionPage.Execute(w, page)
}

// RenderDiscussion writes the confirmation page from positional values.
//
// Deprecated: Use RenderDiscussionPage.
func RenderDiscussion(w http.ResponseWriter, action, source, thread, state, csrfKey, csrf, css string) {
	_ = RenderDiscussionPage(w, DiscussionPage{Action: action, Source: source, Thread: thread, State: state, CSRFField: csrfKey, CSRFToken: csrf, Stylesheet: css})
}
