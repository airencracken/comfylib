// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/analytics"
	"github.com/airencracken/comfylib/autobackup"
)

func TestSummaryEscapesValuesAndShowsOperationalStates(t *testing.T) {
	view := Runtime("<script>secret</script>", autobackup.Status{Enabled: true, LastSuccess: time.Unix(1700000000, 0), LastError: "<img src=x onerror=alert(1)>"})
	view.Metrics = []Metric{{Label: "Members", Value: 12}, {Label: "Storage", Value: 2048, Bytes: true}}
	view.Items = append(view.Items, Item{Name: "Community sign-in", Status: "Disabled", Detail: "<script>run()</script>"})
	html, err := Render(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Instance overview", "2.0 KiB", "Last verified backup", "Restart required", "Apply immediately", "Community sign-in", "&lt;script&gt;", "role=\"alert\""} {
		if !strings.Contains(string(html), want) {
			t.Fatal("missing", want)
		}
	}
	for _, bad := range []string{"<script>", "<img src=x", "Directory", "password=", "client_secret="} {
		if strings.Contains(string(html), bad) {
			t.Fatal("unsafe operational output", bad)
		}
	}
	for _, state := range []autobackup.Status{{}, {Enabled: true}, {Enabled: true, Running: true}} {
		text, err := Render(Runtime("", state))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "<script") {
			t.Fatal("script in summary")
		}
	}
}

func TestMetricFormatsAndNoScriptDependency(t *testing.T) {
	for _, test := range []struct {
		n    int64
		want string
	}{{0, "0 bytes"}, {1 << 10, "1.0 KiB"}, {1 << 20, "1.0 MiB"}, {1 << 30, "1.0 GiB"}, {1 << 40, "1.0 TiB"}} {
		if got := metricValue(Metric{Value: test.n, Bytes: true}); got != test.want {
			t.Fatal(got, test.want)
		}
	}
	html, _ := Render(View{})
	if strings.Contains(string(html), "<script") || strings.Contains(string(html), "fetch(") {
		t.Fatal("summary requires scripting")
	}
}

func TestSuiteSummaryNeverExposesConfiguredValues(t *testing.T) {
	t.Setenv("TEST_SHARED_ISSUER", "https://private.example/?client_secret=do-not-show")
	t.Setenv("TEST_APPS", `[{"id":"witmoot","url":"https://private.example/"}]`)
	text, err := Render(WithSuite(Runtime("", autobackup.Status{}), "TEST_"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"do-not-show", "private.example", "client_secret"} {
		if strings.Contains(string(text), secret) {
			t.Fatal("configuration value leaked", secret)
		}
	}
	if !strings.Contains(string(text), "Configured") {
		t.Fatal("configured state missing")
	}
}

func TestDashboardActivityEscapingAndCommonSections(t *testing.T) {
	activity := analytics.Summary{Started: time.Unix(1700000000, 0), Logins: 2, Views: 3, ActiveMembers: 1, LoginsByAccount: []analytics.Login{{Name: "<script>bad()</script>", Count: 2, Last: time.Unix(1700000000, 0)}}, Popular: []analytics.Content{{Kind: analytics.Post, ID: "<img src=x onerror=bad()>", Views: 3}}}
	view := WithOperations(View{Metrics: []Metric{{Label: "Members", Value: 2}}, Activity: &activity, BackURL: "/settings"}, "1.2.3", autobackup.Status{}, "TEST_")
	html, err := Render(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"comfy-admin-dashboard", "data-comfy-dashboard=", "Instance metrics", "Activity", "Daily activity", "Recent sign-ins by account", "Most viewed content", "Operations", "once per account, item and UTC day", "90 days", "Earlier activity cannot be reconstructed.", "href=\"/settings\"", "&lt;script&gt;"} {
		if !strings.Contains(text, want) {
			t.Fatal("dashboard contract missing", want)
		}
	}
	for _, bad := range []string{"<script>", "<img src=x", "onclick=", "<style"} {
		if strings.Contains(text, bad) {
			t.Fatal("unsafe dashboard markup", bad)
		}
	}
	if view.Activity != &activity || len(view.Metrics) != 1 {
		t.Fatal("operations lost activity")
	}
}
