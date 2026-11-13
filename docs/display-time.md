# Account display timezones

`displaytime` validates IANA timezone names, stores each account's choice and
formats times in it. Timezone data is embedded. The default is UTC.

## Storing the choice

- Witmoot keeps its own account column. Other apps call `Initialize` at
  writable startup, `Read` in their account middleware and `ForgetMember` in
  their account-deletion transaction. `Initialize` rejects a newer schema.
- `ParseForm` rejects repeated fields. `Save` runs your SQL statement with the
  validated name and account ID. Keep your active-account condition in that
  statement; a missing or inactive account returns an error.

## Formatting

- `Formatter` shows instants in the reader's timezone, including daylight
  saving. `ISO` always returns UTC. Keep storage, APIs, exports and analytics
  days in UTC.
- Camera metadata without an offset is not an instant; don't convert it.
- Build templates once with `NewTemplates` and pass the reader's timezone to
  `ExecuteTemplate`. It caches at most 32 timezone variants. Guests and
  invalid saved values get UTC.

## Controls

`RenderPreference` renders the account form. It accepts a same-origin action
and a CSRF field named `csrf` or `csrf_token`. Serve `ui/timezone.js` to add
suggestions and a "Use this device's timezone" button; it never saves on its
own. The form works without JavaScript.

## Testing with your driver

`displaytimetest.Run` checks the storage against your SQLite driver.
