// SPDX-License-Identifier: AGPL-3.0-or-later

// Package admin presents owner-only aggregate metrics and operational summaries.
// Applications own authorization, SQL, and configuration policy. This package
// never collects browser events, reads secrets, or exposes an HTTP endpoint.
package admin

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"

	"github.com/airencracken/comfylib/analytics"
	"github.com/airencracken/comfylib/autobackup"
	"github.com/airencracken/comfylib/displaytime"
	"github.com/airencracken/comfylib/suite"
)

// Metric is an aggregate, without member names, addresses, or content.
type Metric struct {
	Label  string
	Value  int64
	Detail string
	Bytes  bool
}

// Query supplies application-owned SQL returning one non-null integer.
type Query struct {
	Label, SQL, Detail string
	Args               []any
	Bytes              bool
}

// Collect reads all metrics from one database snapshot. On any error it returns
// no partial result. Callers should supply a bounded request context and indexed
// predicates; application SQL must never come from query parameters.
func Collect(ctx context.Context, db *sql.DB, queries []Query) ([]Metric, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	metrics := make([]Metric, 0, len(queries))
	for _, query := range queries {
		var value int64
		if err = tx.QueryRowContext(ctx, query.SQL, query.Args...).Scan(&value); err != nil {
			return nil, fmt.Errorf("metric %s: %w", query.Label, err)
		}
		metrics = append(metrics, Metric{Label: query.Label, Value: value, Detail: query.Detail, Bytes: query.Bytes})
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return metrics, nil
}

// Item describes a configuration source or enabled integration without values.
type Item struct{ Name, Status, Detail string }

// View contains data already authorized by the application for its current owner.
type View struct {
	Timezone string

	Metrics  []Metric
	Items    []Item
	Backup   autobackup.Status
	Version  string
	BackURL  string
	Activity *analytics.Summary
}

// Runtime supplies common backup and configuration explanations. Applications
// append only integrations they actually configured. Secret values are omitted.
func Runtime(version string, backup autobackup.Status) View {
	return View{Version: version, Backup: backup, Items: []Item{
		{Name: "Service configuration", Status: "Restart required", Detail: "Addresses, trusted proxies, backup scheduling and community sign-in settings apply when the service starts."},
		{Name: "Settings saved in this app", Status: "Apply immediately", Detail: "Saved settings take effect immediately unless the setting says otherwise."},
	}}
}

// WithOperations adds the common operational sections without losing collected
// activity, domain metrics or the application's administration link.
func WithOperations(view View, version string, backup autobackup.Status, prefix string) View {
	runtime := Runtime(version, backup)
	view.Version, view.Backup = runtime.Version, runtime.Backup
	view.Items = append(view.Items, runtime.Items...)
	return WithSuite(view, prefix)
}

// WithSuite adds configuration presence without copying URLs, credentials, or
// other configuration values into HTML. It does not probe an external service.
func WithSuite(view View, prefix string) View {
	apps, _ := suite.ParseApps(os.Getenv(prefix + "APPS"))
	navigation := false
	for _, app := range apps {
		if app.ID != strings.ToLower(strings.TrimSuffix(prefix, "_")) {
			navigation = true
		}
	}
	status := func(enabled bool) string {
		if enabled {
			return "Configured"
		}
		return "Disabled"
	}
	view.Items = append(view.Items,
		Item{Name: "App navigation", Status: status(navigation), Detail: "Configured in the service; restart after changing it."},
		Item{Name: "Community sign-in", Status: status(os.Getenv(prefix+"SHARED_ISSUER") != ""), Detail: "Configuration presence only; provider connectivity is checked when signing in."})
	return view
}

func metricValue(metric Metric) string {
	if !metric.Bytes {
		return fmt.Sprint(metric.Value)
	}
	return byteSize(metric.Value)
}

func byteSize(n int64) string {
	for _, unit := range []struct {
		size int64
		name string
	}{{1 << 40, "TiB"}, {1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n >= unit.size {
			return fmt.Sprintf("%.1f %s", float64(n)/float64(unit.size), unit.name)
		}
	}
	return fmt.Sprintf("%d bytes", n)
}

// latency describes a histogram percentile as an upper bound.
func latency(h analytics.Histogram, p float64) string {
	upper, ok := h.Percentile(p)
	switch {
	case !ok:
		return "No requests"
	case upper == analytics.Slower:
		buckets := analytics.LatencyBuckets()
		return "Over " + duration(buckets[len(buckets)-1])
	}
	return "Up to " + duration(upper)
}

func duration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", d.Seconds()), "0"), ".") + " s"
}

func percent(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

var funcs = template.FuncMap{"metricValue": metricValue, "byteSize": byteSize, "latency": latency, "percent": percent, "utc": displaytime.New("UTC").Stamp}

var summaryTemplate = template.Must(template.New("admin").Funcs(funcs).Parse(`
<div class="comfy-admin-dashboard" data-comfy-dashboard="1">
<header class="comfy-admin-heading"><div><h1>Instance metrics</h1><p>How this instance is doing. Only administrators can see this page.</p></div><a href="{{if .BackURL}}{{.BackURL}}{{else}}/{{end}}">Back to administration</a></header>
<section class="comfy-admin-section" aria-labelledby="comfy-overview-title"><h2 id="comfy-overview-title">Instance overview</h2>
{{if .Metrics}}<dl class="comfy-metrics">{{range .Metrics}}<div><dt>{{.Label}}</dt><dd>{{metricValue .}}{{if .Detail}}<p>{{.Detail}}</p>{{end}}</dd></div>{{end}}</dl>{{end}}
</section>
{{with .Activity}}<section class="comfy-admin-section" aria-labelledby="comfy-health-title"><h2 id="comfy-health-title">Site health</h2>
<p class="comfy-activity-window">Past 7 UTC days, including today. Recording began {{utc .Started}}.</p>
<dl class="comfy-metrics"><div><dt>Requests</dt><dd>{{.Requests}}</dd></div><div><dt>Server error rate</dt><dd>{{percent .ErrorRate}}</dd></div><div><dt>Median response time</dt><dd>{{latency .Latency 0.5}}</dd></div><div><dt>95th percentile response time</dt><dd>{{latency .Latency 0.95}}</dd></div><div><dt>Data served</dt><dd>{{byteSize .Bytes}}</dd></div></dl>
<p class="comfy-admin-help">This counts requests, response times and data served for each day and route, and keeps them for 90 days. It does not record who made a request, which page or item it was for, IP addresses or browser details.</p>
<div class="comfy-metrics-table-wrap"><table class="comfy-metrics-table"><caption>Daily traffic</caption><thead><tr><th scope="col">UTC date</th><th scope="col">Requests</th><th scope="col">Server errors</th><th scope="col">95th percentile</th><th scope="col">Data served</th></tr></thead><tbody>{{range .Days}}<tr><th scope="row">{{.Day.Format "2 Jan 2006"}}</th><td>{{.Requests}}</td><td>{{.Status5xx}}</td><td>{{latency .Latency 0.95}}</td><td>{{byteSize .Bytes}}</td></tr>{{end}}</tbody></table></div>
<div class="comfy-metrics-table-wrap"><table class="comfy-metrics-table"><caption>Slowest routes</caption><thead><tr><th scope="col">Route</th><th scope="col">Requests</th><th scope="col">Median</th><th scope="col">95th percentile</th></tr></thead><tbody>{{range .Slowest}}<tr><th scope="row"><code>{{.Pattern}}</code></th><td>{{.Requests}}</td><td>{{latency .Latency 0.5}}</td><td>{{latency .Latency 0.95}}</td></tr>{{else}}<tr><td colspan="4">No requests yet.</td></tr>{{end}}</tbody></table></div>
<div class="comfy-metrics-table-wrap"><table class="comfy-metrics-table"><caption>Routes with errors</caption><thead><tr><th scope="col">Route</th><th scope="col">Requests</th><th scope="col">Client errors</th><th scope="col">Server errors</th></tr></thead><tbody>{{range .Failing}}<tr><th scope="row"><code>{{.Pattern}}</code></th><td>{{.Requests}}</td><td>{{.Status4xx}}</td><td>{{.Status5xx}}</td></tr>{{else}}<tr><td colspan="4">No errors in this period.</td></tr>{{end}}</tbody></table></div>
</section>
<section class="comfy-admin-section" aria-labelledby="comfy-security-title"><h2 id="comfy-security-title">Security</h2>
<dl class="comfy-metrics"><div><dt>Failed sign-ins</dt><dd>{{.FailedSignIns}}</dd></div><div><dt>Rate-limited attempts</dt><dd>{{.RateLimited}}</dd></div><div><dt>Rejected invitation and reset links</dt><dd>{{.RejectedTokens}}</dd></div></dl>
<p class="comfy-admin-help">Daily totals only. No usernames, accounts or IP addresses are stored.</p>
<div class="comfy-metrics-table-wrap"><table class="comfy-metrics-table"><caption>Daily security events</caption><thead><tr><th scope="col">UTC date</th><th scope="col">Failed sign-ins</th><th scope="col">Rate-limited</th><th scope="col">Rejected links</th></tr></thead><tbody>{{range .Days}}<tr><th scope="row">{{.Day.Format "2 Jan 2006"}}</th><td>{{.FailedSignIns}}</td><td>{{.RateLimited}}</td><td>{{.RejectedTokens}}</td></tr>{{end}}</tbody></table></div>
</section>{{end}}
<section class="comfy-admin-section" aria-labelledby="comfy-operations-title"><h2 id="comfy-operations-title">Operations</h2>
{{if .Version}}<p>Running version: <span>{{.Version}}</span></p>{{end}}
<dl class="comfy-operations">{{range .Items}}<div><dt>{{.Name}}</dt><dd>{{.Status}}{{if .Detail}}<p>{{.Detail}}</p>{{end}}</dd></div>{{end}}
<div><dt>Automatic backups</dt><dd>{{if .Backup.Enabled}}{{if .Backup.Running}}Running{{else}}Enabled{{end}}{{else}}Disabled{{end}}
{{if not .Backup.LastSuccess.IsZero}}<p>Last verified backup: {{utc .Backup.LastSuccess}}</p>{{else if .Backup.Enabled}}<p>The first backup hasn't run yet.</p>{{end}}
{{if .Backup.LastError}}<p class="comfy-notice comfy-notice-error" role="alert">Last backup problem: {{.Backup.LastError}}</p>{{end}}
{{if and .Backup.Enabled (not .Backup.NextAttempt.IsZero)}}<p>Next attempt: {{utc .Backup.NextAttempt}}</p>{{end}}</dd></div></dl>
</section></div>`))

var summary, summaryErr = displaytime.NewTemplates(summaryTemplate, func(f displaytime.Formatter) template.FuncMap { return template.FuncMap{"utc": f.Stamp} })

// Render returns escaped HTML for insertion into an application's own template.
// Only call it after the application's owner or administrator check succeeds.
func Render(view View) (template.HTML, error) {
	var out bytes.Buffer
	if summaryErr != nil {
		return "", summaryErr
	}
	if err := summary.ExecuteTemplate(&out, view.Timezone, "admin", view); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil // #nosec G203 -- values are escaped by html/template above.
}
