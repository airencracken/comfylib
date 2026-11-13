# Service configuration

Shipped environment and service examples quote every value, numbers, booleans
and empty strings included:

```sh
APP_DATA_DIR="/var/lib/app"
APP_BASE_URL="https://app.example.org"
APP_SECURE_COOKIES="true"
APP_BACKUP_KEEP="7"
APP_OPTIONAL_VALUE=""
```

- One assignment per line, no spaces around `=`, no `export`.
- Comments go on their own lines. Commented examples use the same format.
- Values are literals. Inside double quotes, escape `"`, `\`, `$` and a backtick
  with a backslash. No variable expansion, command substitution or shell
  operators.
- The first line names the service manager and installed path. OpenRC reads
  `/etc/conf.d/APP`; systemd reads `/etc/APP/APP.env`. Restart the service
  after editing. OpenRC init scripts export the settings themselves.

Existing bare or single-quoted settings still work, and upgrades never rewrite
a user's file.

`tools/check_service_config.py` checks the format. Apps run it as
`scripts/test_service_config.py` in `make check`.
