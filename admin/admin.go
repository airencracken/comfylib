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

	"github.com/airencracken/comfylib/autobackup"
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
	Metrics []Metric
	Items   []Item
	Backup  autobackup.Status
	Version string
}

// Runtime supplies common backup and configuration explanations. Applications
// append only integrations they actually configured. Secret values are omitted.
func Runtime(version string, backup autobackup.Status) View {
	return View{Version: version, Backup: backup, Items: []Item{
		{Name: "Service configuration", Status: "Restart required", Detail: "Addresses, trusted proxies, backup scheduling and community sign-in settings apply when the service starts."},
		{Name: "Settings saved in this app", Status: "Apply immediately", Detail: "Saved settings take effect immediately unless the setting says otherwise."},
	}}
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
	for _, unit := range []struct {
		size int64
		name string
	}{{1 << 40, "TiB"}, {1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if metric.Value >= unit.size {
			return fmt.Sprintf("%.1f %s", float64(metric.Value)/float64(unit.size), unit.name)
		}
	}
	return fmt.Sprintf("%d bytes", metric.Value)
}

var summary = template.Must(template.New("admin").Funcs(template.FuncMap{"metricValue": metricValue, "utc": func(t time.Time) string { return t.UTC().Format("2 Jan 2006, 15:04 UTC") }}).Parse(`
<section class="comfy-admin-section" aria-labelledby="comfy-operations-title"><h2 id="comfy-operations-title">Instance overview</h2>
{{if .Metrics}}<dl class="comfy-metrics">{{range .Metrics}}<div><dt>{{.Label}}</dt><dd>{{metricValue .}}{{if .Detail}}<p>{{.Detail}}</p>{{end}}</dd></div>{{end}}</dl>{{end}}
{{if .Version}}<p>Running version: <span>{{.Version}}</span></p>{{end}}
<dl class="comfy-operations">{{range .Items}}<div><dt>{{.Name}}</dt><dd>{{.Status}}{{if .Detail}}<p>{{.Detail}}</p>{{end}}</dd></div>{{end}}
<div><dt>Automatic backups</dt><dd>{{if .Backup.Enabled}}{{if .Backup.Running}}Running{{else}}Enabled{{end}}{{else}}Disabled{{end}}
{{if not .Backup.LastSuccess.IsZero}}<p>Last verified backup: {{utc .Backup.LastSuccess}}</p>{{else if .Backup.Enabled}}<p>The first verified backup is pending.</p>{{end}}
{{if .Backup.LastError}}<p class="comfy-notice comfy-notice-error" role="alert">Last backup problem: {{.Backup.LastError}}</p>{{end}}
{{if and .Backup.Enabled (not .Backup.NextAttempt.IsZero)}}<p>Next attempt: {{utc .Backup.NextAttempt}}</p>{{end}}</dd></div></dl>
<p class="comfy-admin-help">These totals describe this instance. Recent activity covers the last 7 days. Operational details are visible only to owners and administrators.</p></section>`))

// Render returns escaped HTML for insertion into an application's own template.
// Only call it after the application's owner or administrator check succeeds.
func Render(view View) (template.HTML, error) {
	var out bytes.Buffer
	if err := summary.Execute(&out, view); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil // #nosec G203 -- values are escaped by html/template above.
}
