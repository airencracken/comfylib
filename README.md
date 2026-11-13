# comfylib

Shared Go packages, styles and tools for the Comfyware apps: Folkweave, Imvault,
Songstead, Tidings and Witmoot.

The core module uses only the Go standard library. Each app pins an exact
version. The optional [`oidc`](oidc/README.md) module is versioned separately
and carries its own dependencies.

Build with Go 1.26.11 or later on 1.26, or Go 1.27.4 or later on 1.27.

## Packages

| Package | What it does |
| --- | --- |
| [`admin`](admin) | Renders the owner metrics dashboard and one account's sign-in state. Apps supply queries and access checks. |
| [`analytics`](analytics) | Counts requests, response times and security events per day and route. See [site health metrics](docs/analytics.md). |
| [`analytics/analyticstest`](analytics/analyticstest) | Checks the analytics storage against an app's SQLite driver. |
| [`autobackup`](autobackup) | Schedules compressed private snapshots with bounded retention. |
| [`brandimage`](brandimage) | Bounds uploaded PNG, JPEG and GIF branding images and converts them to PNG. |
| [`clientip`](clientip) | Finds the client address and scheme behind trusted proxies. |
| [`displaytime`](displaytime) | Validates and formats saved account timezones. See [display time](docs/display-time.md). |
| [`invitation`](invitation) | Reports whether an invitation is open, revoked, expired or used up. |
| [`keyfile`](keyfile) | Loads a key from its own file and creates it atomically on first use. |
| [`memberprofile`](memberprofile) | Validates optional names, bios and profile links. |
| [`oidc`](oidc) | Separate module: OpenID Connect sign-in for existing local accounts. |
| [`password`](password) | Reads confirmed passwords from a hidden prompt, stdin or a private file. |
| [`privdrop`](privdrop) | Re-runs admin commands as the service account instead of root. |
| [`profileimage`](profileimage) | Bounds account pictures and makes still and animated versions. |
| [`proxyconfig`](proxyconfig) | Prints a checked Caddy, nginx or Apache site configuration. |
| [`proxyconfig/proxytest`](proxyconfig/proxytest) | Runs those configurations in real nginx and Apache servers. |
| [`ratelimit`](ratelimit) | Bounded in-memory token buckets and fixed windows. |
| [`reference`](reference) | Validates web addresses and prepares Witmoot discussion drafts. |
| [`sandbox`](sandbox) | Confines a service and its children with Bubblewrap. |
| [`setup`](setup) | Serves the first-owner setup page. See [setup](docs/setup.md). |
| [`smtp`](smtp) | Sends transactional mail through a relay that must use TLS. |
| [`suite`](suite) | App navigation, linked sign-in and discussion returns. See [suite](docs/suite.md). |
| [`svcconfig`](svcconfig) | Reads a service's settings the way OpenRC or systemd passes them. See [configuration](docs/configuration.md). |
| [`token`](token) | Mints tokens, hashes them for storage and compares them in constant time. |
| [`ui`](ui) | Shared styles, palettes and browser script. See [theming](docs/theming.md). |
| [`ui/uitest`](ui/uitest) | Browser checks for app test suites. Not served by `ui.Assets()`. |

## Shared UI

Serve `ui.Assets()` at `/static/comfyware/`. Load `accounts.css`,
`components.css` and, for palettes, `palettes.css` after the app stylesheet.
Load `theme.js` and `suite.js` in `<head>` without `defer`. [Theming](docs/theming.md) lists
the tokens and palettes.

## Checks

`make check` runs formatting, vet, cyclomatic complexity, golangci-lint, race
tests, browser helper tests and every mutation table in `mutations/`.
`make test-sandbox` needs `bwrap`, `make test-proxies` needs nginx and Apache,
and `make fuzz-smoke` runs each fuzz target briefly.

Apps run `tools/mutate.py` and the other tools straight from the pinned module
through `scripts/comfy.py`. See [suite maintenance](docs/suite.md).

## Developing alongside an app

Use a `go.work` file in the app's worktree:

```sh
go work init . ../comfylib
```

Never commit a `go.work` file or a `replace` directive in an app. Release builds
resolve comfylib through the Go module proxy.

## Releases

See [release notes](docs/releases.md).

Licensed under AGPL-3.0-or-later. See [LICENSE](LICENSE).
