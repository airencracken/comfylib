# Common Comfyware setup

Applications share two ways to create an owner:

```sh
APP create-owner --username NAME
APP setup
```

The first command uses hidden, confirmed terminal input by default. Explicit
`--password-prompt`, `--password-stdin`, and `--password-file PATH` select the
same sources in every app. Conflicting sources, multiline or oversized input,
and password files accessible to other users are rejected before account writes.
New passwords need at least 12 characters and at most 72 UTF-8 bytes. Applications
retain their existing username rules and preserve all existing accounts.

The second command prints the private key and address for the first-owner
browser page. Start the service and visit `/setup`, enter the key, and choose
your username and confirmed password. The page works without JavaScript and
adapts to narrow screens and system light/dark preferences. Browser setup uses
HTTPS, with HTTP allowed on a loopback host for local use. Keys are never placed
in links, browser responses, or server startup logs.

Installed commands use `svcconfig` and `privdrop` to find the same data directory
and account as the service. An operator may invoke them with sudo; portable
installations run them as their own unprivileged account. The setup key is
created atomically in a private regular `.setup.key` file under the data
directory. Repeated setup commands show the same key while setup is pending.

`Config.Ready` must report whether any owner exists, including suspended owners.
`Config.Create` must check that condition and insert the first owner in one
application-owned transaction, returning `setup.ErrComplete` if another process
has already initialized the instance. It must never promote or overwrite an
existing username. The shared handler checks CSRF, origin, transport, body size,
and the host key before invoking that callback. Owner creation through the CLI
also closes browser setup. The key is removed after browser completion; an
existing owner keeps setup closed even if file cleanup fails.

Use `Handler.Wrap` around the application's production HTTP handler. Health
checks pass through while pending; other routes remain behind setup until an
owner exists. Start automatic backups after `Handler.Wait` returns successfully,
so a deliberately uninitialized service does not create misleading backup errors.
Applications own account validation, database transactions, authentication,
application settings, service files, and legacy command aliases.

The additive API is recorded under `testdata/api`. `tools/check_setup.mjs`
exercises the browser contract against real app binaries, including private host
authorization, first-owner sign-in, replay refusal, no-JavaScript forms, and the
shared account layout assertions at mobile and desktop widths in both themes.
`tools/check_password_cli.py` verifies hidden terminal input and restoration.
