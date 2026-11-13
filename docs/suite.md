# Suite components and maintenance

The stdlib-only `suite` package supplies optional app navigation, bounded OIDC
flow state, explicit identity bindings and discussion returns. `ui/components.css`
and `ui/suite.js` provide native menus, optional form sections and per-app density.
Applications keep their own palettes, templates, sessions and permissions.

The optional, separately versioned `github.com/airencracken/comfylib/oidc`
module uses CoreOS go-oidc to validate signed issuer, audience, expiry and nonce
claims, with OAuth2 PKCE. Import it as `sharedoidc` and call `FromEnvironment`.
The parent module retains its standard-library-only dependency contract. The
adapter never provisions accounts by email; applications own session and access
policy through `suite.AuthHooks`. See [the adapter contract](../oidc/README.md).

Applications import `github.com/airencracken/comfylib/suite` directly. Serve
`ui.Assets()` at `/static/comfyware/` and load `accounts.css`, `components.css` and
`suite.js` after the application stylesheet. Browser tests resolve
`ui/uitest/controls.mjs` from the same pinned module as the application.

## Maintenance commands

Each application has one small `scripts/comfy.py` launcher. It resolves the exact
Comfylib module in `go.mod` with `GOWORK=off` and invokes `tools/app.py`. This works
from a populated module cache with `GOPROXY=off`, including isolated mutation
copies. It never falls back to an adjacent checkout or a moving branch.

```sh
python3 scripts/comfy.py check
python3 scripts/comfy.py service-config
python3 scripts/comfy.py suite-deployment
python3 scripts/comfy.py release-dispatch
python3 scripts/comfy.py test-containers
python3 scripts/comfy.py mutate scripts/mutations/example.json
python3 scripts/comfy.py scan-artifacts dist/app_linux_amd64.tar.gz --scanner /path/to/govulncheck
python3 scripts/comfy.py container-release build --arch amd64 --revision COMMIT
```

Archive staging, registry publication, service configuration and deployment
contracts now have one implementation. Applications retain their Dockerfiles,
Compose and native-service examples, plus `scripts/containers/config.json`.
That strict configuration names the app, previous version, port, temporary space,
owner command and either the `snapshot` or `commands` lifecycle profile. The two
profiles preserve their existing backup, upgrade and restore checks. Publication
still requires both tested architectures and refuses mismatched immutable tags.

`check` rejects copied suite and OIDC packages, browser assets, maintenance tools,
container scripts, module replacements, missing explicit pins and incorrect
launcher wiring. Update the published dependency pins to upgrade the common
implementation; there are no generated copies or SHA-256 sync manifests.
`tools/sync_suite.py` and its adapter templates have been retired.

Run one local audit with machine-readable results and per-command logs:

```sh
python3 tools/audit_suite.py /checkouts --output /tmp/comfyware-audit --mutations --browsers
```

This checks all eight projects, including the website and Gentoo overlay. It runs
vet, race tests, reachable dependency checks, shared integration checks,
mutations and browsers. It does not pull, commit, deploy or publish. Update each
checkout's default branch before starting an audit. All checkouts should be
isolated worktrees. Reports remain local; commands that need network access must
be run in an environment with that access.

For a coordinated published dependency update, first inspect the plan:

```sh
python3 tools/rollout_comfylib.py v0.3.0 /checkouts/imvault /checkouts/songstead /checkouts/witmoot /checkouts/folkweave /checkouts/tidings
```

Add `--oidc-version v0.1.0` to update the optional protocol adapter as well.
Add `--apply` to download the explicit version, update clean module manifests and
run each app's race and shared integration checks. The tool stops on failure,
retains local changes for review and never commits or publishes. It does not
introduce workspace replacements or unpublished dependency pins.

Release workflows scan each actual archive executable with govulncheck in binary
mode after artifact verification and before publication. The scanner reads only
regular archive members into a temporary file, refuses unsafe member paths and
fails if an archive has no recognizable executable. Source vulnerability checks
still run independently in CI.

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
