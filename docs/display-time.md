# Account display timezones

The `displaytime` package supplies IANA validation, saved-account controls,
formatters and a bounded template cache. It imports only the Go standard library
and embeds timezone data for static binaries. UTC is the default.

Applications authorize their native account routes, retain CSRF protection, and
load only the current reader's preference. `ParseForm` rejects duplicated scalar
fields. `Save` binds the validated name and account ID to application-owned SQL
that must retain the native active-account predicate. A missing or inactive
account fails instead of silently succeeding.

Witmoot retains its existing account column. Applications without a native column
call `Initialize` only during writable startup, `Read` in their account middleware,
and `ForgetMember` inside their native account-deletion transaction. The SQLite
namespace is independently versioned; future versions are rejected. Initialization
is transactional and repeated startup preserves preferences. Backups of the
application database include preferences. Read-only/current-schema commands do
not initialize the namespace.

`Formatter` applies a reader's timezone to display dates, including daylight
saving and local calendar boundaries. `ISO` always emits UTC. Keep native storage,
API and export contracts unchanged, and keep analytics day buckets in UTC. Camera
metadata without an offset cannot be interpreted as an instant.

Freeze an unexecuted template set once with `NewTemplates`, bind the formatter
functions, and pass the request's preferred timezone to `ExecuteTemplate`. The
cache contains at most 32 immutable timezone variants, never user data or CSRF
tokens. Guests and invalid old preferences fall back to UTC.

`RenderPreference` escapes all values, accepts a same-origin form action, and
allows only the native `csrf` or `csrf_token` field. Serve `ui/timezone.js` beside
the shared styles. The picker can suggest browser-supported zones and fill the
device timezone; it never saves automatically. The native POST route and server
rendering also work without JavaScript.

The test contract `displaytime/displaytimetest.Run` exercises the actual SQLite
driver supplied by an app. It checks isolation, strict schema inputs, native
active-account updates, rollback, failure injection, namespace versions, and
transactional deletion. Unit, property, mutation and concurrent cache tests
cover DST, calendar boundaries, fractional offsets, escaping and machine UTC.
The native metrics browser gate also checks timezone controls in all five apps,
with JavaScript enabled and disabled, persistence across sessions, device
detection without autosaving, and unchanged UTC reporting days.
