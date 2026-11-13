# Release notes

## Unreleased (0.7.0)

### Analytics measure site health, not people

- `analytics` now counts requests per UTC day and route pattern: status
  classes, a latency histogram and bytes served, through
  `Monitor.Middleware`. It also counts failed sign-ins, rate-limited attempts
  and rejected invitation or reset links. It stores no account or content IDs,
  usernames, IP addresses, user agents, referrers, paths or query strings.
- `Initialize` drops the per-account sign-in and per-item view table. Use
  `RemoveDetail` to run that step in your own migration.
- The admin dashboard shows Site health (requests, error rate, median and 95th
  percentile response time, data served, slowest routes, routes with errors)
  and Security. It no longer shows sign-ins by account, most viewed content,
  content views or active accounts.
- The dashboard shows "The last backup failed. Check the server log." instead
  of the raw backup error (`autobackup.Status.Summary`). The runner still logs
  the error.
- `admin.RenderAccountSupport` shows one account's last sign-in and rate-limit
  state. `ratelimit.Window.Peek` and `ratelimit.Limiter.Peek` read that state
  without spending an attempt.
- `analyticstest.Run` checks the new storage. `tools/check_metrics.mjs`
  accepts dashboards with or without the Site health section and checks styles
  against each app's tokens.

### One token set and palettes

- Shared styles read only `--comfy-*` tokens with neutral defaults. Headings,
  sizes and corners come from tokens; the metrics dashboard no longer has its
  own palette.
- `palettes.css` adds `sage` and `slate`, each with light and dark values.
  `ui.ParsePalette`, `ui.SiteDefault` and `ui.PaletteAttrs` let an owner set a
  site palette.
- `theme.js` and `ui.ThemePicker` replace each app's theme script and picker:
  System, Light or Dark plus a palette, applied before drawing.
- `suite.js` adds a Copy link button to inputs marked `data-comfy-copy`.
- The setup page stylesheet is readable and uses the tokens.

### Error pages

- The new `problem` package writes plain-language pages for 400, 403, 404, 413,
  422, 429 and 500 inside the app's layout, sends JSON to API clients, keeps
  `HX-Refresh` on expired htmx forms and logs internal detail capped at 512
  bytes.

### Plain limits

- `password.Validate` measures bcrypt's real limit (`MaxBytes`, 72 bytes) and
  returns plain errors: `ErrTooShort` ("Use at least 12 characters."),
  `ErrTooLong` ("Use a shorter password.") and `ErrInvalid`. `password.Fits`
  checks the byte limit at sign-in.
- `ui.Size` formats byte counts in decimal units ("5 MB") for user copy.

### Maintenance

- `scripts/comfy.py check-docs` (`tools/check_docs.py`) checks that the README
  and release notes agree with `VERSION`.

### Suite

- `suite.DiscussionStore` holds pending discussion returns.
  `suite.RenderDiscussionPage` takes a `DiscussionPage`.

### Deprecated, removed in 0.8.0

- `analytics.RecordLogin`, `RecordView`, `RecordViews`, `Track` and
  `ForgetMember` do nothing. `Kind` and its constants, `Account`, `Login`,
  `Content`, `Event`, `Label`, `MemberID` and `NumericID` remain unused.
  `Summary.Logins`, `ActiveMembers`, `Views`, `LoginsByAccount`, `Popular` and
  `Daily.Logins` and `Daily.Views` are always empty. `Collect` ignores its
  accounts query.
- The fallback from tokens to older app variables. See
  [theming](theming.md#deprecated-fallback).
- `suite.PrepareDiscussion`, `CheckDiscussion`, `FinishDiscussion` and
  `RenderDiscussion`.

### Breaking changes

All exported Go names still compile. These behave differently:

- Upgrading deletes stored per-account sign-ins and content views.
- Successful sign-ins and content views are no longer counted, so dashboards
  that read the deprecated fields show zeros.
- Dashboard headings and captions changed; browser tests that look for
  "Activity" or "Recent sign-ins by account" must change.
- Shared CSS no longer defines `--comfy-ink`, `--comfy-panel`, `--comfy-line` or
  `--invitation-*`. Apps that read them must use the new tokens.
- Without a token mapping or palette, the dashboard uses the app's older
  variables or neutral system colors instead of its fixed blue-grey palette.
- The dashboard no longer shows the raw backup error.
- `password.Validate` errors have new text; code that matched the old string
  should use `errors.Is` with `ErrTooShort`, `ErrTooLong` or `ErrInvalid`.
- `analyticstest.Run` checks the new tables, and `check_metrics.mjs` no longer
  accepts the per-account and per-item tables.

## 0.6.0

Adds saved display timezones: strict IANA validation, embedded timezone data,
account controls, device detection without autosave, server-side formatters and
a bounded template cache. Admin times follow the reader; reporting days stay
UTC. See [display time](display-time.md).

## 0.5.0

Adds sign-in and content-view analytics with 90-day retention, and one shared
admin dashboard layout and palette checked in all five apps.

## 0.4.3

Requests the selected module version explicitly during cold-cache preflight, so
Go returns origin metadata on its first download.

## 0.4.2

Makes browser checks a required publication gate and passes the exact source
reference. Checks native archives against their declared file layouts.

## 0.4.1

Fixes metrics browser tooling for apps with bare module names. Container
contracts follow pinned shared workflow references.

## 0.4.0

Adds `admin` and `ratelimit`, native metrics browser contracts, pinned shared
workflows (`containers.yml`, `browser.yml`), release preparation, artifact
checks and source-bound release preflight.

## 0.3.0

- Apps import `suite` and serve `ui.Assets()` directly; generated copies and
  sync manifests are gone.
- Adds shared maintenance commands run through `scripts/comfy.py`: service
  configuration, deployment, release checks, archive scans, mutations and
  container releases.
- Adds `--oidc-version` to coordinated dependency rollouts.

## OIDC module 0.1.0

`github.com/airencracken/comfylib/oidc` replaces app-local OpenID Connect
adapters. Tagged `oidc/v0.1.0`. See [its README](../oidc/README.md).

## 0.2.0

- Adds app navigation, OpenID Connect flow state, explicit identity links and
  discussion returns to `suite`.
- Adds shared menus, focus styles, form disclosures and density preferences.
- Adds audit, dependency rollout and release archive scanning tools.
- Redacts service configuration values from errors.
- Requires Go 1.26.11 or later.

## 0.1.8

Adds `password.Flags` and `password.Source` for account commands, and the
`setup` package for the first-owner page. See [setup](setup.md).

## 0.1.7

Adds `ui.Assets()` with `accounts.css`, the browser helpers in `ui/uitest`, and
`invitation.State.Status`.

## 0.1.6

Adds `autobackup`.

## 0.1.5

Adds `memberprofile`.

## 0.1.4

Adds `profileimage`: 2 MiB input, 512 by 512 pixels, up to 64 GIF frames, with
still and animated versions.

## 0.1.3

Adds `password.WithHiddenInput`, which turns off terminal echo before the first
prompt and restores it afterwards.

## 0.1.2

Adds `password.Confirm` and `brandimage.Normalize` (2 MiB, 2048 by 2048 pixels,
one PNG out).

## 0.1.1

Adds `reference` for Witmoot discussion handoffs, and `token.SessionCSRF`.
