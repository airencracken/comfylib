// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
)

// Suggestions is a fresh fallback list for browsers without Intl timezone catalogs.
func Suggestions() []string {
	return []string{"UTC", "Africa/Cairo", "Africa/Johannesburg", "America/Anchorage", "America/Argentina/Buenos_Aires", "America/Chicago", "America/Denver", "America/Los_Angeles", "America/Mexico_City", "America/New_York", "America/Sao_Paulo", "America/Toronto", "Asia/Dubai", "Asia/Hong_Kong", "Asia/Kolkata", "Asia/Seoul", "Asia/Shanghai", "Asia/Singapore", "Asia/Tokyo", "Australia/Adelaide", "Australia/Brisbane", "Australia/Perth", "Australia/Sydney", "Europe/Berlin", "Europe/London", "Europe/Paris", "Pacific/Auckland", "Pacific/Honolulu"}
}

// Preference contains only the signed-in reader's timezone form fields.
type Preference struct{ Action, CSRF, CSRFField, Name, Error, Notice string }

var preference = template.Must(template.New("timezone").Funcs(template.FuncMap{"suggestions": Suggestions}).Parse(`<section class="panel comfy-timezone" id="display-timezone"><h2>Times and dates</h2><form action="{{.Action}}" method="post" hx-boost="false"><input type="hidden" name="{{.CSRFField}}" value="{{.CSRF}}"><label for="timezone">Display timezone</label><input id="timezone" name="timezone" list="timezones" maxlength="64" value="{{.Name}}" aria-describedby="timezone-help" autocomplete="off" required><datalist id="timezones">{{range suggestions}}<option value="{{.}}"></option>{{end}}</datalist><p id="timezone-help">UTC by default. Search for a city or enter a timezone such as America/Los_Angeles. Your choice is saved across devices. Reporting days remain UTC.</p><div class="comfy-timezone-actions"><button type="button" class="secondary" data-device-timezone hidden>Use this device's timezone</button><button type="submit">Save timezone</button></div>{{if .Error}}<p class="comfy-notice comfy-notice-error" role="alert">{{.Error}}</p>{{end}}{{if .Notice}}<p class="comfy-notice" role="status">{{.Notice}}</p>{{end}}</form></section>`))

// RenderPreference returns escaped markup for a same-origin native account form.
func RenderPreference(p Preference) (template.HTML, error) {
	u, err := url.Parse(p.Action)
	if err != nil || !strings.HasPrefix(p.Action, "/") || strings.HasPrefix(p.Action, "//") || strings.ContainsAny(p.Action, "\\\r\n") || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrTimezone
	}
	if p.CSRFField == "" {
		p.CSRFField = "csrf"
	}
	if p.CSRFField != "csrf" && p.CSRFField != "csrf_token" {
		return "", ErrTimezone
	}
	var out bytes.Buffer
	if err := preference.Execute(&out, p); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil // #nosec G203 -- html/template escapes all account fields.
}
