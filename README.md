# comfylib

Shared Go packages for the Comfyware apps, [Imvault](https://github.com/airencracken/imvault)
and [Witmoot](https://github.com/airencracken/witmoot).

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
| [`clientip`](clientip) | Finds the client address behind trusted reverse proxies, groups IPv6 clients by /64 for rate limits, and tells whether the original request used HTTPS. |
| [`keyfile`](keyfile) | Loads a key from its own file, creating it atomically on first use, in raw or hex form. |
| [`privdrop`](privdrop) | Re-runs administrative commands as the service account, refusing to run them as root. |
| [`proxyconfig`](proxyconfig) | Prints a validated Caddy, nginx or Apache site configuration from an app's own examples. |
| [`proxyconfig/proxytest`](proxyconfig/proxytest) | Runs those configurations in real nginx and Apache servers for an app's integration tests. |
| [`sandbox`](sandbox) | Confines a service and its child processes with Bubblewrap, and reaps orphans when the service is PID 1. |
| [`smtp`](smtp) | Sends transactional mail through a relay with required TLS, header-injection checks, and no logging of message bodies. |
| [`svcconfig`](svcconfig) | Reads a service's settings, data directory and account the way OpenRC and systemd pass them. |
| [`token`](token) | Mints random tokens, hashes them for storage, and compares them in constant time. |

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
