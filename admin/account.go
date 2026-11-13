// SPDX-License-Identifier: AGPL-3.0-or-later
package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"time"

	"github.com/airencracken/comfylib/displaytime"
)

// AccountSupport is sign-in state for one account, shown on that account's
// own admin page to help with support requests. Fill it from the app's
// session records and its rate limiter (for example ratelimit.Window.Peek)
// for that account only. Never collect it for several accounts to list,
// compare or rank them.
type AccountSupport struct {
	LastSignIn time.Time     // zero if the account has never signed in
	Limited    bool          // sign-in is currently refused by a rate limit
	RetryAfter time.Duration // when Limited, the time until sign-in is allowed
}

var accountTemplate = template.Must(template.New("account").Funcs(template.FuncMap{"utc": displaytime.New("UTC").Stamp, "wait": wait}).Parse(
	`<dl class="comfy-operations comfy-account-support"><div><dt>Last sign-in</dt><dd>{{if .LastSignIn.IsZero}}Never{{else}}{{utc .LastSignIn}}{{end}}</dd></div><div><dt>Sign-in limit</dt><dd>{{if .Limited}}Sign-in is paused for {{wait .RetryAfter}}.{{else}}Not limited{{end}}</dd></div></dl>`))

var accountTemplates, accountErr = displaytime.NewTemplates(accountTemplate, func(f displaytime.Formatter) template.FuncMap { return template.FuncMap{"utc": f.Stamp} })

// wait rounds a retry delay up to whole minutes.
func wait(d time.Duration) string {
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes <= 1 {
		return "about a minute"
	}
	return fmt.Sprintf("about %d minutes", minutes)
}

// RenderAccountSupport returns escaped HTML for one account's sign-in state,
// with times in the reader's timezone. Call it only after the app's
// administrator check succeeds.
func RenderAccountSupport(timezone string, account AccountSupport) (template.HTML, error) {
	if accountErr != nil {
		return "", accountErr
	}
	var out bytes.Buffer
	if err := accountTemplates.ExecuteTemplate(&out, timezone, "account", account); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil // #nosec G203 -- escaped by html/template above.
}
