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

## Developing alongside an app

Use a `go.work` file in the app's worktree rather than a `replace` directive:

```sh
go work init . ../comfylib
```

`go.work` is ignored by git. Never commit a `replace` directive or a `go.work`
file in an app; release builds, the Gentoo ebuilds, and Docker images all
resolve comfylib through the Go module proxy.

Licensed under AGPL-3.0-or-later. See [LICENSE](LICENSE).
