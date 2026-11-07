# Service configuration convention

All shipped Comfyware environment and service configuration examples use
double quotes for every value, including numbers, booleans, and empty strings:

```sh
APP_DATA_DIR="/var/lib/app"
APP_BASE_URL="https://app.example.org"
APP_SECURE_COOKIES="true"
APP_BACKUP_KEEP="7"
APP_OPTIONAL_VALUE=""
```

Use one assignment per line, without spaces around `=` or an `export` prefix.
Put explanatory comments on their own lines. Commented examples use the same
format as active settings. These are strings in an environment; the application
interprets numbers, durations, and booleans after reading them.

Each file identifies its service manager and installed path in its first line.
OpenRC reads `/etc/conf.d/APP`; systemd reads `/etc/APP/APP.env`. Edit the file
for the manager you run, then restart that service. OpenRC init scripts export
the supported settings; users do not need to add `export` to their configuration.
Neither manager depends on the other manager's file.

Values are literals. Inside double quotes, escape a literal double quote,
backslash, dollar sign, or backtick with a backslash. Do not use variable
expansion, command substitution, or shell operators in shared examples.

This convention governs examples and newly shipped defaults. Existing bare or
single-quoted settings remain valid, and upgrades preserve local configuration.
Formatting changes never migrate or overwrite a user's values.

`tools/check_service_config.py` checks this convention and includes adversarial
and property tests. Apps keep the same checker as `scripts/test_service_config.py`
and run it during `make check`. Comfylib's configuration tests verify that quoted
literal values agree between the OpenRC and systemd readers.
