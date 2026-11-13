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
	for _, want := range []string{"Instance overview", "2.0 KiB", "Last verified backup", "Restart required", "Apply immediately", "Community sign-in", "&lt;script&gt;", "role=\"alert\"", autobackup.FailureSummary} {
		if !strings.Contains(string(html), want) {
			t.Fatal("missing", want)
		}
	}
	for _, bad := range []string{"<script>", "<img src=x", "img src", "onerror", "Directory", "password=", "client_secret="} {
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

func TestDashboardShowsHealthAndSecurityWithoutPeople(t *testing.T) {
	var latency analytics.Histogram
	for i := 0; i < 17; i++ {
		latency.Observe(20 * time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		latency.Observe(time.Minute)
	}
	traffic := analytics.Traffic{Requests: 20, Status2xx: 18, Status5xx: 2, Bytes: 4096, Latency: latency}
	activity := analytics.Summary{
		Started:  time.Unix(1700000000, 0),
		Traffic:  traffic,
		Security: analytics.Security{FailedSignIns: 4, RateLimited: 2, RejectedTokens: 1},
		Days:     []analytics.Daily{{Day: time.Unix(1700000000, 0), Traffic: traffic, Security: analytics.Security{FailedSignIns: 4}}},
		Slowest:  []analytics.Route{{Pattern: "GET /<script>bad()</script>", Traffic: traffic}},
		Failing:  []analytics.Route{{Pattern: "POST /login", Traffic: traffic}},
	}
	view := WithOperations(View{Metrics: []Metric{{Label: "Members", Value: 2}}, Activity: &activity, BackURL: "/settings"}, "1.2.3", autobackup.Status{}, "TEST_")
	html, err := Render(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"comfy-admin-dashboard", "Instance metrics", "Site health", "Daily traffic", "Slowest routes", "Routes with errors", "Security",
		"Failed sign-ins", "Rate-limited attempts", "Rejected invitation and reset links", "Daily security events", "10.0%", "Up to 25 ms", "Over 10 s", "4.0 KiB",
		"90 days", "does not record who made a request", "No usernames, accounts or IP addresses", "Operations", `href="/settings"`, "&lt;script&gt;"} {
		if !strings.Contains(text, want) {
			t.Fatal("dashboard contract missing", want)
		}
	}
	for _, gone := range []string{"<script>", "Recent sign-ins by account", "Most viewed content", "Content views", "Active accounts", "Successful sign-ins", "<style"} {
		if strings.Contains(text, gone) {
			t.Fatal("dashboard shows", gone)
		}
	}
	if view.Activity != &activity || len(view.Metrics) != 1 {
		t.Fatal("operations lost activity")
	}
	empty, err := Render(View{Activity: &analytics.Summary{}})
	if err != nil || !strings.Contains(string(empty), "No requests yet.") || !strings.Contains(string(empty), "No errors in this period.") {
		t.Fatal("empty health tables", err)
	}
}

func TestDurationAndLatencyLabels(t *testing.T) {
	for d, want := range map[time.Duration]string{5 * time.Millisecond: "5 ms", time.Second: "1 s", 2500 * time.Millisecond: "2.5 s", 10 * time.Second: "10 s"} {
		if got := duration(d); got != want {
			t.Fatal(d, got, want)
		}
	}
	if latency(analytics.Histogram{}, .5) != "No requests" || percent(0) != "0.0%" {
		t.Fatal("empty labels")
	}
}

func TestAccountSupportShowsOneAccount(t *testing.T) {
	html, err := RenderAccountSupport("America/Los_Angeles", AccountSupport{LastSignIn: time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC), Limited: true, RetryAfter: 61 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"Last sign-in", "PST", "Sign-in is paused for about 2 minutes."} {
		if !strings.Contains(text, want) {
			t.Fatal("missing", want, text)
		}
	}
	html, err = RenderAccountSupport("UTC", AccountSupport{})
	if err != nil || !strings.Contains(string(html), "Never") || !strings.Contains(string(html), "Not limited") {
		t.Fatal("new account", html, err)
	}
	if wait(time.Second) != "about a minute" || wait(0) != "about a minute" {
		t.Fatal("short waits")
	}
	// An unknown saved timezone falls back like the dashboard does.
	if html, err := RenderAccountSupport("Not/AZone", AccountSupport{LastSignIn: time.Unix(1700000000, 0)}); err != nil || !strings.Contains(string(html), "Last sign-in") {
		t.Fatal("unknown timezone", html, err)
	}
}
