// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type call struct {
	query string
	args  []any
}

// executor records statements instead of running them.
type executor struct {
	calls []call
	err   error
}

func (e *executor) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	e.calls = append(e.calls, call{q, append([]any{}, args...)})
	return driver.RowsAffected(1), e.err
}

// recordDriver accepts every statement and keeps it, so tests can replay the
// SQL a Monitor writes in a real SQLite database.
type recordDriver struct{}
type recordConn struct{}
type recordTx struct{}

var recorded struct {
	sync.Mutex
	calls []call
	fail  bool
}

func (recordDriver) Open(string) (driver.Conn, error)  { return recordConn{}, nil }
func (recordConn) Close() error                        { return nil }
func (recordConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (recordConn) Begin() (driver.Tx, error) {
	recorded.Lock()
	defer recorded.Unlock()
	if recorded.fail {
		return nil, errors.New("storage unavailable")
	}
	return recordTx{}, nil
}
func (recordConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	recorded.Lock()
	defer recorded.Unlock()
	values := make([]any, len(args))
	for i, a := range args {
		values[i] = a.Value
	}
	recorded.calls = append(recorded.calls, call{q, values})
	return driver.RowsAffected(1), nil
}
func (recordTx) Commit() error   { return nil }
func (recordTx) Rollback() error { return nil }
func init()                      { sql.Register("comfy-analytics-record-test", recordDriver{}) }

func takeRecorded() []call {
	recorded.Lock()
	defer recorded.Unlock()
	calls := recorded.calls
	recorded.calls = nil
	return calls
}

// sqlite runs script with the schema and the given statements loaded, using
// Python's standard-library SQLite.
func sqlite(t *testing.T, script string, values map[string]any) {
	t.Helper()
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	prelude := "import json,sqlite3,sys\nv=json.load(sys.stdin); c=sqlite3.connect(':memory:')\n"
	cmd := exec.CommandContext(t.Context(), "python3", "-c", prelude+script)
	cmd.Stdin = bytes.NewReader(data)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SQLite contract: %v\n%s", err, output)
	}
}

func statements(calls []call) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		out = append(out, map[string]any{"sql": c.query, "args": append([]any{}, c.args...)})
	}
	return out
}

// nearestRank is the textbook percentile of sorted samples.
func nearestRank(sorted []time.Duration, p float64) time.Duration {
	return sorted[int(math.Ceil(p*float64(len(sorted))))-1]
}

func TestHistogramPercentileBracketsTheTrueValue(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	bounds := LatencyBuckets()
	for trial := 0; trial < 500; trial++ {
		var h Histogram
		samples := make([]time.Duration, 1+random.Intn(200))
		for i := range samples {
			samples[i] = time.Duration(random.ExpFloat64() * float64(time.Duration(1+random.Intn(4000))*time.Millisecond))
			h.Observe(samples[i])
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		if h.Count() != int64(len(samples)) {
			t.Fatal("count", h.Count(), len(samples))
		}
		for _, p := range []float64{.01, .25, .5, .9, .95, .99, 1, random.Float64()*0.999 + .001} {
			got, ok := h.Percentile(p)
			want := nearestRank(samples, p)
			if !ok || got < want {
				t.Fatalf("p%.3f = %v, below true %v", p, got, want)
			}
			// The result is the smallest bound at or above the true value.
			index := sort.Search(len(bounds), func(i int) bool { return bounds[i] >= want })
			if index == len(bounds) && got != Slower || index < len(bounds) && got != bounds[index] {
				t.Fatalf("p%.3f = %v, want bucket of %v", p, got, want)
			}
		}
		for _, pair := range [][2]float64{{.01, .5}, {.5, .95}, {.95, 1}, {random.Float64() * .5, .5 + random.Float64()*.5}} {
			low, okLow := h.Percentile(pair[0])
			high, okHigh := h.Percentile(pair[1])
			if okLow && okHigh && low > high {
				t.Fatal("percentiles not monotonic", pair, low, high)
			}
		}
	}
	var empty Histogram
	if _, ok := empty.Percentile(.5); ok {
		t.Fatal("empty histogram has a percentile")
	}
	var one Histogram
	one.Observe(time.Hour)
	for _, p := range []float64{0, -1, 1.0001, math.NaN(), math.Inf(1)} {
		if _, ok := one.Percentile(p); ok {
			t.Fatal("out-of-range percentile accepted", p)
		}
	}
	if got, _ := one.Percentile(.5); got != Slower {
		t.Fatal("overflow bucket", got)
	}
	var edge Histogram
	edge.Observe(5 * time.Millisecond)
	edge.Observe(-time.Second)
	if edge[0] != 2 {
		t.Fatal("bucket bounds are inclusive and negative durations are fastest", edge)
	}
	if len(LatencyBuckets()) != len(Histogram{})-1 {
		t.Fatal("bucket count")
	}
	LatencyBuckets()[0] = time.Hour
	if LatencyBuckets()[0] != 5*time.Millisecond {
		t.Fatal("caller changed shared bounds")
	}
}

func TestTrafficStatusClassesAndErrorRate(t *testing.T) {
	var traffic Traffic
	for _, status := range []int{100, 200, 204, 301, 304, 404, 429, 500, 503, 600, 999} {
		traffic.observe(status, -5, time.Millisecond)
	}
	if traffic.Requests != 11 || traffic.Status2xx != 3 || traffic.Status3xx != 2 || traffic.Status4xx != 2 || traffic.Status5xx != 4 || traffic.Bytes != 0 {
		t.Fatalf("%+v", traffic)
	}
	if rate := traffic.ErrorRate(); math.Abs(rate-4.0/11) > 1e-9 || (Traffic{}).ErrorRate() != 0 {
		t.Fatal("error rate", rate)
	}
}

func TestRouteLabelsNeverCarryPathsOrQueries(t *testing.T) {
	for pattern, want := range map[string]string{
		"":                            Unmatched,
		"GET /posts/{id}":             "GET /posts/{id}",
		strings.Repeat("a", 128):      strings.Repeat("a", 128),
		strings.Repeat("a", 129):      Other,
		"GET /café":                   Other,
		"GET /a\nSet-Cookie: x":       Other,
		"GET /\x00":                   Other,
		"GET /<script>alert</script>": "GET /<script>alert</script>",
	} {
		if got := routeLabel(pattern); got != want {
			t.Errorf("routeLabel(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func newTestMonitor(t *testing.T) (*Monitor, *time.Time) {
	t.Helper()
	db, err := sql.Open("comfy-analytics-record-test", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	m := NewMonitor(db)
	m.now = func() time.Time { return now }
	m.last = now
	takeRecorded()
	return m, &now
}

func TestMonitorCountsPatternsStatusesBytesAndPanics(t *testing.T) {
	m, now := newTestMonitor(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		*now = now.Add(30 * time.Millisecond)
		switch r.PathValue("id") {
		case "teapot":
			w.WriteHeader(999)
		case "boom":
			panic("handler failure")
		case "empty":
		default:
			_, _ = w.Write([]byte("hello"))
		}
	})
	h := m.Middleware(mux)
	serve := func(target string) {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	for _, target := range []string{"/posts/1", "/posts/2?token=secret", "/posts/teapot", "/posts/boom", "/posts/empty", "/missing/../../etc/passwd", "/nothing?q=private", "/posts/%2e%2e%2f%00?ref=x"} {
		serve(target)
	}
	post := m.pending[routeKey{day(*now), "GET /posts/{id}"}]
	missing := m.pending[routeKey{day(*now), Unmatched}]
	if post == nil || missing == nil || len(m.pending) != 2 {
		t.Fatalf("labels %v", m.pending)
	}
	if post.Requests != 6 || post.Status2xx != 4 || post.Status5xx != 2 || post.Bytes != 15 || post.Latency[3] != 6 {
		t.Fatalf("post traffic %+v", *post)
	}
	// The unclean path is redirected and the unknown one is not found.
	if missing.Requests != 2 || missing.Status3xx != 1 || missing.Status4xx != 1 {
		t.Fatalf("unmatched traffic %+v", *missing)
	}
	for key := range m.pending {
		for _, leak := range []string{"secret", "passwd", "teapot", "ref=", "%2e"} {
			if strings.Contains(key.route, leak) {
				t.Fatal("request detail stored", key.route)
			}
		}
	}
}

func TestMonitorBoundsDistinctRoutes(t *testing.T) {
	m, now := newTestMonitor(t)
	for i := 0; i < maxRoutes*3; i++ {
		m.observe(*now, "GET /r"+strings.Repeat("x", i%100)+string(rune('a'+i%26))+strings.Repeat("y", i/100), 200, 1, time.Millisecond)
	}
	if len(m.pending) > maxRoutes+1 || m.pending[routeKey{day(*now), Other}] == nil {
		t.Fatal("route table unbounded", len(m.pending))
	}
	var total int64
	for _, traffic := range m.pending {
		total += traffic.Requests
	}
	if total != maxRoutes*3 {
		t.Fatal("requests lost while bounding routes", total)
	}
}

func TestMonitorConcurrentRequests(t *testing.T) {
	m, _ := newTestMonitor(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("a")) })
	mux.HandleFunc("POST /b", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	h := m.Middleware(mux)
	var group sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		group.Go(func() {
			for i := 0; i < 100; i++ {
				method, target := http.MethodGet, "/a"
				if i%2 == 1 {
					method, target = http.MethodPost, "/b"
				}
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
				if i%10 == 0 {
					if err := m.Flush(context.Background()); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	group.Wait()
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var requests, bytes, notFound int64
	for _, c := range takeRecorded() {
		if strings.HasPrefix(c.query, "INSERT INTO comfy_analytics_routes") {
			requests += c.args[2].(int64)
			notFound += c.args[5].(int64)
			bytes += c.args[7].(int64)
		}
	}
	if requests != 3200 || notFound != 1600 || bytes != 1600 {
		t.Fatal("concurrent counts", requests, notFound, bytes)
	}
}

func TestMonitorFlushesOncePerIntervalAndKeepsCountsOnFailure(t *testing.T) {
	m, now := newTestMonitor(t)
	h := m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if len(takeRecorded()) != 0 {
		t.Fatal("flushed before the interval")
	}
	recorded.Lock()
	recorded.fail = true
	recorded.Unlock()
	if err := m.Flush(t.Context()); err == nil {
		t.Fatal("storage failure hidden")
	}
	recorded.Lock()
	recorded.fail = false
	recorded.Unlock()
	*now = now.Add(FlushInterval)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	calls := takeRecorded()
	if len(calls) != 1 || calls[0].args[2].(int64) != 2 {
		t.Fatal("pending counts lost or flushed twice", calls)
	}
	if err := m.Flush(t.Context()); err != nil || len(takeRecorded()) != 0 {
		t.Fatal("empty flush wrote rows", err)
	}
}

func TestSecurityCountsAreBoundAndValidated(t *testing.T) {
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	e := &executor{}
	for _, record := range []func(context.Context, Executor, time.Time) error{RecordFailedSignIn, RecordRateLimited, RecordRejectedToken} {
		if err := record(t.Context(), e, at); err != nil {
			t.Fatal(err)
		}
		if record(t.Context(), e, time.Unix(-1, 0)) == nil {
			t.Fatal("negative time accepted")
		}
		if record(t.Context(), &executor{err: errors.New("storage failure")}, at) == nil {
			t.Fatal("storage error swallowed")
		}
	}
	if len(e.calls) != 3 {
		t.Fatal(e.calls)
	}
	for _, c := range e.calls {
		if len(c.args) != 1 || c.args[0] != day(at) {
			t.Fatal("only the day is stored", c)
		}
	}
}

func TestDeprecatedRecordingStoresNothing(t *testing.T) {
	at := time.Now()
	e := &executor{}
	if RecordLogin(t.Context(), e, "7", at) != nil || RecordView(t.Context(), e, Post, "7", "9", at) != nil || ForgetMember(t.Context(), e, "7") != nil || len(e.calls) != 0 {
		t.Fatal("deprecated call stored data", e.calls)
	}
	if err := RecordViews(t.Context(), nil, []Event{{Kind: Post, Member: "1", Item: "2"}}, at); err != nil {
		t.Fatal(err)
	}
	resolved := false
	h := Track(nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, func(*http.Request) []Event { resolved = true; return nil })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/posts/1", nil))
	if resolved || w.Body.String() != "ok" {
		t.Fatal("Track still resolves views")
	}
	if NumericID("001") != "1" || NumericID("-1") != "" || MemberID(5) != "5" || Label(Post) != "Post" || Label("x") != "Content" || (Content{Kind: Image, ID: "3"}).Description() != "Image 3" {
		t.Fatal("deprecated helpers changed")
	}
}

// Real SQLite: the schema, migration, route upserts, security counts and
// retention behave as Collect expects.
func TestSQLiteStorageMigrationAndRetention(t *testing.T) {
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	m, now := newTestMonitor(t)
	for i := 0; i < 3; i++ {
		m.observe(*now, "GET /a", 200, 10, time.Duration(i)*20*time.Millisecond)
	}
	m.observe(*now, "GET /a", 503, 0, 3*time.Second)
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.observe(*now, "GET /a", 404, 7, time.Millisecond)
	if err := m.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	routes := takeRecorded()
	e := &executor{}
	if err := RemoveDetail(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDetail(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	migrate := e.calls
	e = &executor{}
	for _, age := range []int{0, 0, 89, 90} {
		if err := RecordFailedSignIn(t.Context(), e, at.Add(-time.Duration(age)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordRateLimited(t.Context(), e, at); err != nil {
		t.Fatal(err)
	}
	if err := RecordRejectedToken(t.Context(), e, at); err != nil {
		t.Fatal(err)
	}
	if err := Prune(t.Context(), e, at); err != nil {
		t.Fatal(err)
	}
	sqlite(t, `
c.executescript("CREATE TABLE comfy_analytics_activity(day INTEGER, kind TEXT, member TEXT, item TEXT, count INTEGER, last_at INTEGER); INSERT INTO comfy_analytics_activity VALUES(1,'login','7','',3,1)")
c.executescript(v['schema'])
for q in v['migrate']: c.execute(q['sql'],q['args'])
assert c.execute("SELECT count(*) FROM sqlite_master WHERE name='comfy_analytics_activity'").fetchone()[0]==0, 'old table kept'
for q in v['routes']+v['security']: c.execute(q['sql'],q['args'])
row=c.execute("SELECT requests,status_2xx,status_4xx,status_5xx,bytes,latency_0,latency_2,latency_3,latency_9 FROM comfy_analytics_routes WHERE route='GET /a'").fetchone()
assert row==(5,3,1,1,37,2,1,1,1), row
assert c.execute("SELECT failed_sign_ins,rate_limited,rejected_tokens FROM comfy_analytics_security WHERE day=?",(v['day'],)).fetchone()==(2,1,1)
assert c.execute("SELECT count(*) FROM comfy_analytics_security").fetchone()[0]==2, 'retention boundary'
for bad in ["INSERT INTO comfy_analytics_routes(day,route,requests,status_2xx,status_3xx,status_4xx,status_5xx,bytes,%s) VALUES(1,'',1,0,0,0,0,0,%s)","INSERT INTO comfy_analytics_routes(day,route,requests,status_2xx,status_3xx,status_4xx,status_5xx,bytes,%s) VALUES(1,'x',0,0,0,0,0,0,%s)"]:
    try:
        c.execute(bad % (v['latency'], ','.join(['0']*12))); raise SystemExit('invalid row accepted')
    except sqlite3.IntegrityError: pass
plan=c.execute("EXPLAIN QUERY PLAN DELETE FROM comfy_analytics_routes WHERE day<?",(0,)).fetchall()
assert any('INDEX' in str(p) for p in plan), plan
`, map[string]any{"schema": schema, "migrate": statements(migrate), "routes": statements(routes), "security": statements(e.calls), "day": day(at), "latency": latencyColumns})
}

func TestLatencyColumnsMatchTheHistogram(t *testing.T) {
	columns := strings.Split(latencyColumns, ",")
	if len(columns) != len(Histogram{}) {
		t.Fatal("columns", len(columns))
	}
	for i, column := range columns {
		if column != "latency_"+strconv.Itoa(i) || strings.Count(schema, " "+column+" INTEGER NOT NULL") != 1 {
			t.Fatal("column", i, column)
		}
	}
}
