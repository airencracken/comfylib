# comfylib

Shared Go packages for the Comfyware apps, [Imvault](https://github.com/airencracken/imvault)
and [Witmoot](https://github.com/airencracken/witmoot), plus
[Songstead](https://github.com/airencracken/songstead),
[Folkweave](https://github.com/airencracken/folkweave), and
[Tidings](https://github.com/airencracken/tidings).

It holds the security-sensitive plumbing the apps need and should get right
once: Bubblewrap confinement, systemd and OpenRC configuration lookup, SMTP
sending, key files, tokens, client addresses behind trusted proxies, and
reverse-proxy configuration. Application permissions, domain storage and native screens stay in each app.
Shared forms and admin dashboards use common layout assets and browser contracts.

The core module uses only the Go standard library, so it adds no further modules to
an application's dependency tree. It is versioned as v0 while its API settles; each
app pins an exact version.

Use a current Go security patch when building: Go 1.26.11 or later on the
1.26 series, or Go 1.27.4 or later on the 1.27 series.

## Packages

| Package | What it does |
| --- | --- |
| [`analytics`](analytics) | Shared SQLite activity storage, successful sign-ins, deduplicated authorized content opens, retention and reporting. |
| [`admin`](admin) | Presents owner-only aggregate metrics and operational summaries; apps own queries and access checks. |
| [`ratelimit`](ratelimit) | Bounded fixed-window and token-bucket budgets with deterministic clocks. |
| [`autobackup`](autobackup) | Schedules verified private snapshots, prefers zstd with gzip fallback, and bounds archive retention and copy size. |
| [`brandimage`](brandimage) | Bounds and normalizes uploaded PNG, JPEG and GIF branding images. |
| [`password`](password) | Reads confirmed passwords through an app-supplied hidden terminal reader. |
| [`clientip`](clientip) | Finds the client address behind trusted reverse proxies, groups IPv6 clients by /64 for rate limits, and tells whether the original request used HTTPS. |
| [`invitation`](invitation) | Describes invitation availability at an explicit time; apps retain atomic redemption and permissions. |
| [`ui`](ui) | Embeds shared account and invitation styles, with browser assertions under `ui/uitest`. |
| [`keyfile`](keyfile) | Loads a key from its own file, creating it atomically on first use, in raw or hex form. |
| [`memberprofile`](memberprofile) | Validates optional names, plain-text bios and labeled HTTP/HTTPS links without fetching them. Apps own visibility and storage. |
| [`profileimage`](profileimage) | Bounds account pictures and supplies still and animated renditions. |
| [`privdrop`](privdrop) | Re-runs administrative commands as the service account, refusing to run them as root. |
| [`proxyconfig`](proxyconfig) | Prints a validated Caddy, nginx or Apache site configuration from an app's own examples. |
| [`proxyconfig/proxytest`](proxyconfig/proxytest) | Runs those configurations in real nginx and Apache servers for an app's integration tests. |
| [`reference`](reference) | Validates web addresses and prepares explicit browser discussion drafts without networking or automatic posting. |
| [`sandbox`](sandbox) | Confines a service and its child processes with Bubblewrap, and reaps orphans when the service is PID 1. |
| [`smtp`](smtp) | Sends transactional mail through a relay with required TLS, header-injection checks, and no logging of message bodies. |
| [`svcconfig`](svcconfig) | Reads a service's settings, data directory and account the way OpenRC and systemd pass them. |
| [`token`](token) | Mints random tokens, hashes them for storage, and compares them in constant time, and derives session-bound CSRF tokens. |

## Checks

Shipped service examples follow the [configuration convention](docs/configuration.md):
`KEY="value"` for every value, no required `export`, and an explicit manager and
installed path. Existing user configuration remains supported unchanged.

`make check` runs formatting, vet, cyclomatic complexity, golangci-lint, the
race-enabled tests, the mutation engine's self-test, and every mutation table
under `mutations/`. `make test-sandbox` needs a working `bwrap`;
`make test-proxies` needs nginx and Apache; `make fuzz-smoke` runs every fuzz
target briefly. CI runs all of them.

`tools/mutate.py` applies each mutation in a table to a throwaway copy of the
tree and requires the named tests to fail. The apps keep their own tables and
a tiny launcher that runs the engine directly from the pinned module.

## Developing alongside an app

Use a `go.work` file in the app's worktree rather than a `replace` directive:

```sh
go work init . ../comfylib
```

`go.work` is ignored by git. Never commit a `replace` directive or a `go.work`
file in an app; release builds, the Gentoo ebuilds, and Docker images all
resolve comfylib through the Go module proxy.

Licensed under AGPL-3.0-or-later. See [LICENSE](LICENSE).

## Discussion references (v0.1.1)

`reference.URL`, `reference.Draft`, `reference.Handoff` and `reference.Read`
validate ordinary web addresses and prepare an explicit Witmoot browser draft.
Songstead and Imvault use the same URL and text limits; Witmoot validates the
handoff again and requires its normal posting flow. No networking, tokens,
service discovery or plugin machinery is added. Do not put private notes or
private album metadata in a handoff. The exported API golden records the
additive package. [v0.1.1](https://github.com/airencracken/comfylib/releases/tag/v0.1.1)
is published and used by Songstead, Witmoot and Imvault.

The same release adds `token.SessionCSRF(session, purpose)`. It
preserves the existing Witmoot and Imvault HMAC-SHA256 outputs for their
application purpose strings and rejects empty inputs. No existing exported
API is removed, and the library still uses only the Go standard library.

## Shared administration helpers (v0.1.2)

`password.Confirm` handles two labeled password prompts and propagates reader
and writer failures without printing secrets. Applications keep terminal echo
control and their own password strength policy.

`brandimage.Normalize` accepts PNG, JPEG and GIF uploads up to 2 MiB and
2048 by 2048 pixels. It validates dimensions before decoding and emits one PNG,
preserving transparency while removing animation and metadata. Songstead,
Imvault and Witmoot use the same normalization policy.

`profileimage.Normalize` adds an account-picture policy: 2 MiB encoded input,
512 by 512 pixels, at most 64 GIF frames, and 4 MiB per output rendition.
It re-encodes a GIF animation and a PNG still for viewer motion preferences;
compressed frame counts and dimensions are checked before animation decoding.

`password.WithHiddenInput` disables Linux terminal echo before the first prompt
is displayed and keeps it disabled through confirmation. It restores the original
terminal state on success, failure and panic, without changing other terminal flags.
Wrap the entire `Confirm` call with it when using a terminal password reader.

## Shared invitation and account UI (v0.1.7)

`ui.Assets()` exposes a read-only filesystem containing `accounts.css`. Serve it
from `/static/comfyware/` and load `/static/comfyware/accounts.css` after the
application stylesheet. The styles preserve each application's palette through
CSS custom properties and align account forms, invitation controls and responsive
records. Templates, application routes and content remain application-owned.

`invitation.State.Status(now)` describes open, revoked, expired and used-up
invitations. Revocation wins over expiry, and expiry wins over exhausted uses.
Expiry includes the exact deadline; a nil expiry means never and a nonpositive
maximum means unlimited. `Status.Label()` supplies a sentence-case label. This is
a display helper; redemption still needs each app's permission checks and atomic
storage transaction.

Browser suites resolve this exact pinned Go module with `go list -m -f '{{.Dir}}'
github.com/airencracken/comfylib`. Import `ui/uitest/layout.mjs` for driver-neutral
DOM checks or `ui/uitest/invitations.mjs` for the Playwright invitation flow. The
Playwright caller supplies `path`, `revokePath`, `app` and optional `screenshots`;
the helper adds no Playwright dependency. CDP callers evaluate the same serialized
DOM functions. The default matrix covers 320, 390, 768 and 1280 pixels, both color
schemes and JavaScript enabled and disabled. Browser helpers are excluded from
`ui.Assets()`.

`make test-ui` requires Node and runs the helper unit tests plus actual mutations
of width limits, overflow checks, control height, contrast, breakpoints and
structure. `make check` includes it. The Go module still uses only the standard
library.

## Common setup

`password.Flags` and `password.Source` supply one hidden-prompt, stdin, and
private-file experience for account commands. The `setup` package supplies the
host-authorized first-owner browser page and a private setup-key command.
Applications retain atomic ownership checks in their own databases. See the
[common setup contract](docs/setup.md) and its real-binary browser test harness.

Shared suite controls, optional app navigation, explicitly linked community sign-in,
discussion returns and coordinated audit/release tooling are described in
[Suite components and maintenance](docs/suite.md).

The [0.2.0 release notes](docs/releases.md) describe the new suite helpers and
patched compiler requirement.

The optional [`comfylib/oidc`](oidc/README.md) companion module supplies the OIDC
protocol adapter and has its own dependency manifest and release tags. Importing
the core module does not pull in its dependencies. Shared application maintenance
tools run directly from the pinned core module; see [suite maintenance](docs/suite.md).

## Consistent administration and activity 0.5.0

The shared admin dashboard now owns its heading, layout, type scale, cards,
tables and neutral light/dark palette. Applications retain their surrounding
navigation and enforce active admin/owner permissions before collecting a report.
The native browser contract asserts exact styles and widths in every app.

`analytics` owns the additive, separately versioned SQLite schema and its indexes,
atomic sign-in recording, concurrent view deduplication, page batches, expiry,
account-deletion cleanup and bounded read snapshots. It imports only the Go
standard library; applications supply their existing SQLite driver. Runtime
collection stores internal IDs, never IP addresses or browser identifiers.
See [activity definitions and integration](docs/analytics.md).

The test suite exercises the emitted SQL with Python 3's standard-library SQLite
and with the applications' real SQLite driver. No database driver is added to the
core module's dependency manifest.

## Shared foundation 0.4.0

The `admin` package renders aggregate metrics and owner-only operational status.
Applications provide SQL and authorize their own routes. The collector uses one
bounded database snapshot and returns no partial report on failure. Summaries
show configuration sources and restart behavior without including secret values.

`ratelimit` supplies both bounded token buckets and expiry-ordered fixed windows.
Each app chooses its attempts, interval, capacity and eviction policy. Successful
sign-ins can refund attempts without extending the window.

The shared browser runtime provides isolated ports, readiness, process cleanup,
required-browser checks, and owner/member/guest contracts. Native metrics pages
are checked in both themes, at four widths, with and without JavaScript.

Application workflows pin `.github/workflows/containers.yml` and `browser.yml` to
an immutable commit. Release preflight verifies that this commit is the origin
of the published module pin. License preparation, native/source/package artifact
checks, and the command-profile Debian lifecycle run from `tools/releases`.
Application-specific package recovery and sandbox checks remain with their apps.

Run `python3 scripts/comfy.py release-plan --require-clean --cold-cache` in an app
before creating its release tag. `--output .release/plan.json` records input
hashes and the source commit; `--verify-plan` refuses changed source or metadata.
The suite catalog is maintained in the overlay's `release-catalog.json`; use
`--catalog ../comfyware/release-catalog.json --suite-root ..` to validate app
versions, published module pins, website mirrors and overlay recipe targets.
