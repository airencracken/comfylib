# Site health and security metrics

The `analytics` package counts how the site is doing, the way a web server log
would. It never records who did something or what they looked at.

## What is stored

Per UTC day and ServeMux route pattern (for example `GET /posts/{id}`):
requests, responses by status class (2xx, 3xx, 4xx, 5xx), a latency histogram
and bytes written. Per UTC day: failed sign-ins, rate-limited attempts and
rejected invitation or reset links.

It stores no account or content IDs, usernames, IP addresses, user agents,
referrers, paths or query strings. Rows are deleted after 90 days.

## Adopting it

1. Call `analytics.Initialize(ctx, db, time.Now())` when the database opens for
   writing. It creates the tables and drops the per-account table that earlier
   versions kept (`RemoveDetail` does only that step, for use inside your own
   migration).
2. Create one `analytics.NewMonitor(db)` and wrap your `*http.ServeMux` with
   `monitor.Middleware(mux)`. Wrap the mux itself: the route label comes from
   `Request.Pattern`, which the mux sets. Requests no pattern matched are
   counted as `unmatched`. Call `monitor.Flush(ctx)` on shutdown.
3. Wrap the outer handler with `analytics.Maintenance(db, handler)` to prune
   expired rows once a day.
4. Call `analytics.RecordFailedSignIn` when a sign-in is rejected,
   `analytics.RecordRateLimited` when a rate limit refuses an attempt, and
   `analytics.RecordRejectedToken` when an invitation or reset token is
   invalid, expired or used.
5. Pass `analytics.Collect(ctx, db, "", time.Now())` to the dashboard as
   `admin.View.Activity`.

Delete from app code: calls to `RecordLogin`, `RecordView`, `RecordViews`,
`Track` and `ForgetMember`, the accounts query passed to `Collect`, and any
view resolvers or content-ID canonicalization written for them. These remain as
deprecated no-ops for one release.

## Reading the numbers

Latency percentiles are reported as the upper bound of a histogram bucket
(5 ms up to 10 s, then "over 10 s"), so the true value is at or below the
figure shown. The error rate counts 5xx responses; status codes above 599 count
as server errors. A handler that panics is counted as a 500.

The Monitor keeps counts in memory and writes them at most once a minute, after
a request. Counts not yet written are lost if the process stops without
`Flush`.

## Account support

`admin.RenderAccountSupport` shows one account's last sign-in and whether a rate
limit is refusing it. Fill `admin.AccountSupport` from your session records and
`ratelimit.Window.Peek` (or `Limiter.Peek`) for that account only. Do not build
lists or rankings of accounts from it.

## Testing with your driver

`analyticstest.Run(t, db)` checks the storage against your SQLite driver. Give
it a dedicated empty database.
