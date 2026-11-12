# Native admin activity metrics

Only active application administrators may read the dashboard. Some apps call
that role owner. Moderators, ordinary members and guests do not receive the
report. Apps retain their own authentication, role checks and content permissions;
Comfylib exposes no HTTP reporting endpoint of its own.

## Counting rules

The report covers today and the preceding six UTC calendar days. The displayed
recording date comes from initialization of the analytics namespace. Upgrading
does not invent earlier sign-ins or content views.

- A successful sign-in is recorded in the transaction that commits its session.
  Both local and verified community sign-in use this path. Pending or failed
  credentials, incomplete second factors and session rotations do not count.
- A view is a successful authenticated content open. A unique database constraint
  counts each account, content kind, item and UTC day once. Refreshes, concurrent
  requests and alternate numeric route spellings cannot inflate that count.
- Lists, feeds, thumbnails, background assets, API calls, HEAD requests, failed
  responses and signed-out visits do not produce view events.
- Witmoot counts the nonremoved posts rendered on the requested topic page.
  Explicit full-image opens count separately from the embedded image fetches.
  Imvault distinguishes images and video clips. Songstead counts recommendation
  detail pages; Folkweave counts post details and original photo opens; Tidings
  counts authorized story details.

Recent sign-ins resolve current account names at report time; names are never
copied into the event table. Recent accounts and popular items are limited to
20 rows each. Content is identified by kind and internal ID; private titles and
bodies are not copied into activity storage. Daily totals remain aggregates.

## Storage and lifecycle

`Initialize` atomically creates the `comfy_analytics_meta` and
`comfy_analytics_activity` namespace with its own version. These additive tables
do not change application tables or the app's migration version. Normal writable
opens initialize it; read-only inspection/backup opens do not.

Retention covers 90 UTC days. Expired rows are pruned at startup and on the first
served request of a new UTC day, independently of visiting the admin page. Idle
or stopped applications prune on their next startup/request. Application backups
include these SQLite tables and keep their existing archive-retention policy.
Restoring a pre-analytics database starts a fresh namespace.

Application account-deletion transactions call `ForgetMember` so retained activity
is removed with the account. Suspending an existing account prevents new sign-ins
and report access without rewriting legitimate past aggregate activity.

## Integration and failure behavior

Call `RecordLogin` with the session transaction after all authentication factors
and account-state checks. Call `Track` inside the application's authentication
middleware and only around real detail/open handlers. Its event resolver supplies
canonical, authorized IDs after a successful response. Topic pages use
`RecordViews` to commit all displayed posts in one write transaction.

Domain queries and account visibility remain app-owned constants. The shared
collector reads account names and activity in one read-only, five-second snapshot;
any failed query produces no partial report. Application dashboards fail as a
whole if their domain or activity section cannot be collected.

Content-view recording has a short timeout and logs collection failures without
changing a successful content response. Sign-in activity commits atomically with
its session. The renderer escapes labels, names and content identifiers, works
without scripts, and uses one shared stylesheet. Browser contracts cover exact
appearance, both themes, four widths, role boundaries and forged role parameters.
