# Suite components and maintenance

## Packages and assets

`suite` provides app navigation, OpenID Connect flow state, explicit identity
links and discussion returns. Apps keep their own sessions and permissions
through `suite.AuthHooks`; sign-in never creates an account or matches by
email.

Serve `ui.Assets()` at `/static/comfyware/` and load the styles after your own.
See [theming](theming.md). Browser tests import `ui/uitest/controls.mjs` from
the same pinned module.

The OpenID Connect adapter lives in the separate
[`oidc` module](../oidc/README.md).

## Discussion returns

Create one `suite.DiscussionStore` per process. `Prepare` adds a return address
to a reviewed Witmoot handoff, `Check` confirms the return belongs to the same
session, item and forum, and `Finish` removes it after the app saves the link.
Returns expire after 20 minutes; a store holds at most 1024, and 16 per session.
`RenderDiscussionPage` shows the confirmation page.

## Maintenance commands

Each app has a small `scripts/comfy.py` launcher. It finds the exact comfylib
version in `go.mod` (with `GOWORK=off`, and with `GOPROXY=off` from a filled
module cache) and runs `tools/app.py`. It never uses a nearby checkout or a
branch.

```sh
python3 scripts/comfy.py check
python3 scripts/comfy.py check-docs
python3 scripts/comfy.py service-config
python3 scripts/comfy.py suite-deployment
python3 scripts/comfy.py release-dispatch
python3 scripts/comfy.py test-containers
python3 scripts/comfy.py mutate scripts/mutations/example.json
python3 scripts/comfy.py scan-artifacts dist/app_linux_amd64.tar.gz --scanner /path/to/govulncheck
python3 scripts/comfy.py container-release build --arch amd64 --revision COMMIT
```

Apps keep their Dockerfiles, Compose and service examples, and
`scripts/containers/config.json`. That file names the app, previous version,
port, temporary space, owner command and lifecycle profile (`snapshot` or
`commands`). Publishing needs both architectures and refuses to replace a tag.

`check-docs` checks that the README's "Current release" line, the newest
release-notes heading and documented container tags match `VERSION`, that the
README has no release-note sections, and that it ends with License or Support.
Notes for the next version are allowed when marked unreleased: an
"Unreleased" heading, "unreleased" in the version heading, or a first line
after the heading that starts with "Unreleased". Add it to `make check`.

`check` rejects copied suite packages, assets or tools, `replace` directives,
missing pins and wrong launcher wiring. To upgrade, change the pinned version.

## Audits and rollouts

```sh
python3 tools/audit_suite.py /checkouts --output /tmp/comfyware-audit --mutations --browsers
```

This runs vet, race tests, dependency checks, shared integration checks,
mutations and browsers across all eight projects, and writes a report with
per-command logs. It never pulls, commits, deploys or publishes. Update each
checkout first.

```sh
python3 tools/rollout_comfylib.py v0.3.0 /checkouts/imvault /checkouts/songstead /checkouts/witmoot /checkouts/folkweave /checkouts/tidings
```

This prints the plan. `--oidc-version v0.1.0` also updates the OIDC module.
`--apply` updates clean module files and runs each app's checks; it stops on
the first failure and never commits.

Release workflows scan each archive's executable with govulncheck before
publishing. The scanner refuses unsafe archive paths and archives without an
executable.

## Releasing an app

App workflows pin `.github/workflows/containers.yml` and `browser.yml` to a
commit. Before tagging, run:

```sh
python3 scripts/comfy.py release-plan --require-clean --cold-cache
```

`--output .release/plan.json` records input hashes and the source commit, and
`--verify-plan` refuses changed sources. Add `--catalog
../comfyware/release-catalog.json --suite-root ..` to check app versions,
module pins, website mirrors and overlay recipes against the overlay's catalog.
