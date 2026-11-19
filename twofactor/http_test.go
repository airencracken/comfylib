// SPDX-License-Identifier: AGPL-3.0-or-later

package twofactor

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFormRejectsAmbiguousCSRF(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		values url.Values
		valid  bool
	}{
		{"valid", url.Values{"csrf": {"fixture"}, "code": {"123456"}}, true},
		{"missing", url.Values{"code": {"123456"}}, false},
		{"wrong", url.Values{"csrf": {"other"}}, false},
		{"duplicate csrf", url.Values{"csrf": {"fixture", "fixture"}}, false},
		{"duplicate code", url.Values{"csrf": {"fixture"}, "code": {"123456", "654321"}}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, LoginPath+"?csrf=fixture", strings.NewReader(fixture.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := validForm(r, "fixture"); got != fixture.valid {
				t.Fatalf("valid form = %v", got)
			}
		})
	}
}

func TestFactorTemplatesEscapeFormValues(t *testing.T) {
	const malicious = `"><script>alert(1)</script>`
	for _, data := range []view{
		{Prompt: true, CSRF: malicious, Error: malicious},
		{Secret: malicious, CSRF: malicious},
		{Enabled: true, Codes: []string{malicious}, CSRF: malicious},
	} {
		var out strings.Builder
		if err := forms.Execute(&out, data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "<script>") || !strings.Contains(out.String(), "&lt;script&gt;") {
			t.Fatal("template did not escape form data")
		}
	}
}
