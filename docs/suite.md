# Suite components and maintenance

The stdlib-only `suite` package supplies optional app navigation, bounded OIDC
flow state, explicit identity bindings and discussion returns. `ui/components.css`
and `ui/suite.js` provide native menus, optional form sections and per-app density.
Applications keep their own palettes, templates, sessions and permissions.

The OIDC protocol adapter lives in `tools/oidc_provider.go.txt`; applications use
CoreOS go-oidc to validate signed issuer/audience/expiry/nonce claims and OAuth2
PKCE. Comfylib itself retains its standard-library-only dependency contract.
No email-based account provisioning is performed.

Until apps can upgrade to a published release containing these helpers, maintain
identical local copies using:

```sh
python3 tools/sync_suite.py /checkouts/imvault /checkouts/songstead /checkouts/witmoot /checkouts/folkweave /checkouts/tidings
python3 tools/sync_suite.py --check /checkouts/imvault /checkouts/songstead /checkouts/witmoot /checkouts/folkweave /checkouts/tidings
```

The tool copies regression tests and browser assets and writes SHA-256 manifests.
Each app checks its manifest in CI. A canonical comparison also checks the adapter
and tooling templates. Never edit synchronized copies independently; change the
source here, regenerate, run the app suites and review all diffs together.

Run one local audit with machine-readable results and per-command logs:

```sh
python3 tools/audit_suite.py /checkouts --output /tmp/comfyware-audit --mutations --browsers
```

This checks all eight projects, including the website and Gentoo overlay. It runs
vet, race tests, reachable dependency checks, synchronization checks,
mutations and browsers. It does not pull, commit, deploy or publish. Update each
checkout's default branch before starting an audit. All checkouts should be
isolated worktrees. Reports remain local; commands that need network access must
be run in an environment with that access.

For a coordinated published dependency update, first inspect the plan:

```sh
python3 tools/rollout_comfylib.py v0.1.8 /checkouts/imvault /checkouts/songstead /checkouts/witmoot /checkouts/folkweave /checkouts/tidings
```

Add `--apply` to download the explicit version, update clean module manifests and
run each app's race and synchronization checks. The tool stops on failure,
retains local changes for review and never commits or publishes. It does not
introduce workspace replacements or unpublished dependency pins.

Release workflows scan each actual archive executable with govulncheck in binary
mode after artifact verification and before publication. The scanner reads only
regular archive members into a temporary file, refuses unsafe member paths and
fails if an archive has no recognizable executable. Source vulnerability checks
still run independently in CI.
