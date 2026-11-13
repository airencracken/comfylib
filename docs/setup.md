# First-owner setup

Every app offers two ways to create the first owner:

```sh
APP create-owner --username NAME
APP setup
```

## The command line

`create-owner` asks for a hidden, confirmed password by default.
`--password-prompt`, `--password-stdin` and `--password-file PATH` pick the
source. It rejects conflicting sources, multiline or oversized input, and
password files other users can read. Passwords need at least 12 characters
and at most 72 UTF-8 bytes.

Installed commands use `svcconfig` and `privdrop` to act on the same data
directory and account as the service, so they work under sudo.

## The setup page

`APP setup` prints a private key and the page address. Start the service, open
`/setup`, enter the key and choose a username and password. The page needs
HTTPS, except on a loopback host, and works without JavaScript. The key is
stored in `.setup.key` in the data directory and never appears in links,
responses or logs. Running `setup` again shows the same key until an owner
exists, and the file is deleted once one does.

## Integrating

- `Config.Ready` reports whether any owner exists, suspended owners included.
- `Config.Create` checks that and inserts the owner in one transaction. It
  returns `setup.ErrComplete` if another process got there first, and never
  promotes or overwrites an existing username.
- The handler checks CSRF, origin, transport, body size and the key before
  calling `Create`. Creating an owner from the command line also closes the
  page.
- Wrap your production handler with `Handler.Wrap`. Health checks pass through;
  other routes wait for setup. Start automatic backups after `Handler.Wait`
  returns.

`tools/check_setup.mjs` runs the page against real app binaries, and
`tools/check_password_cli.py` checks hidden terminal input.
