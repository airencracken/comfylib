// SPDX-License-Identifier: AGPL-3.0-or-later

package twofactor

import "html/template"

var forms = template.Must(template.New("two-factor").Parse(`
{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}
{{if .Prompt}}
<p>Enter a code from your authenticator app or use a recovery code.</p>
<form method="post" action="/login/2fa">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <label for="factor-code">Authenticator or recovery code</label>
  <input id="factor-code" name="code" autocomplete="one-time-code" maxlength="24" required autofocus>
  <button type="submit">Sign in</button>
</form>
<p><a href="/login">Start sign-in again</a></p>
{{else}}
{{if .Codes}}
<section><h2>Save your recovery codes</h2>
<p>Each code works once. Store these somewhere safe. They are shown only now.</p>
<ul>{{range .Codes}}<li><code>{{.}}</code></li>{{end}}</ul></section>
{{end}}
{{if .Enabled}}
<p>Two-factor sign-in is on. Recovery codes left: {{.Count}}.</p>
<form method="post" action="/account/two-factor">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <label for="factor-password">Current password</label>
  <input id="factor-password" type="password" name="password" autocomplete="current-password" required>
  <label for="factor-code">Authenticator or recovery code</label>
  <input id="factor-code" name="code" autocomplete="one-time-code" maxlength="24" required>
  <button name="action" value="renew">Replace recovery codes</button>
  <button name="action" value="disable">Turn off two-factor sign-in</button>
</form>
{{else if .Secret}}
<p><a href="{{.URI}}">Open authenticator app</a> or enter this setup key in your app.</p>
<p><code id="authenticator-key">{{.Secret}}</code></p>
<form method="post" action="/account/two-factor">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <label for="factor-code">Six-digit authenticator code</label>
  <input id="factor-code" name="code" inputmode="numeric" pattern="[0-9]{6}" autocomplete="one-time-code" maxlength="6" required>
  <button name="action" value="enable">Turn on two-factor sign-in</button>
</form>
{{else}}
<p>Use an authenticator app to protect your account with a second sign-in step.</p>
<form method="post" action="/account/two-factor">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <label for="factor-password">Current password</label>
  <input id="factor-password" type="password" name="password" autocomplete="current-password" required>
  <button name="action" value="start">Set up authenticator</button>
</form>
{{end}}
<p><a href="/account">Back to your account</a></p>
{{end}}
`))
