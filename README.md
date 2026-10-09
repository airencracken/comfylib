# comfylib

Shared Go packages for the Comfyware apps, [Imvault](https://github.com/airencracken/imvault)
and [Witmoot](https://github.com/airencracken/witmoot), plus
[Songstead](https://github.com/airencracken/songstead).

It holds the security-sensitive plumbing both apps need and should get right
once: Bubblewrap confinement, systemd and OpenRC configuration lookup, SMTP
sending, key files, tokens, client addresses behind trusted proxies, and
reverse-proxy configuration. Application policy, storage schemas, and user
interfaces stay in each app.

comfylib uses only the Go standard library, so it adds no further modules to
either app's dependency tree. It is versioned as v0 while its API settles; each
app pins an exact version.

## Packages

| Package | What it does |
| --- | --- |
| [`brandimage`](brandimage) | Bounds and normalizes uploaded PNG, JPEG and GIF branding images. |
| [`password`](password) | Reads confirmed passwords through an app-supplied hidden terminal reader. |
| [`clientip`](clientip) | Finds the client address behind trusted reverse proxies, groups IPv6 clients by /64 for rate limits, and tells whether the original request used HTTPS. |
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

`make check` runs formatting, vet, cyclomatic complexity, golangci-lint, the
race-enabled tests, the mutation engine's self-test, and every mutation table
under `mutations/`. `make test-sandbox` needs a working `bwrap`;
`make test-proxies` needs nginx and Apache; `make fuzz-smoke` runs every fuzz
target briefly. CI runs all of them.

`tools/mutate.py` applies each mutation in a table to a throwaway copy of the
tree and requires the named tests to fail. The apps keep their own tables and
a synced copy of the script.

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
