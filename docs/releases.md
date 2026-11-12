# Comfylib 0.5.0

Adds shared first-party sign-in and content-view analytics with bounded retention, atomic sessions and page batches, concurrent deduplication, account-deletion cleanup, and snapshot reporting. Admin dashboards share their entire layout, typography, neutral theme palette and tables; real browser checks enforce the same appearance across all five apps. Core runtime dependencies remain standard-library-only. See [analytics](analytics.md) for the counting and privacy rules.

# Comfylib 0.4.3

Requests the selected module version explicitly during cold-cache preflight so Go returns immutable origin metadata on its first download. Adds regression checks for exact version selection, origin rejection and offline consumer verification.

# Comfylib 0.4.2

Makes browser checks a required publication gate and forwards the exact requested source reference. Validates native deployment archives against their declared file layouts while retaining complete source archive checks. Adds workflow, archive and publication regression tests.

# Comfylib 0.4.1

Fixes native metrics browser tooling for applications with bare module names, and makes container contracts follow immutable shared workflow references. Adds regression coverage for all five module names and the delegated workflow source.

# Comfylib 0.4.0

Adds the admin and rate-limit packages, native metrics browser contracts, shared immutable workflows, release preparation and artifact validation, and source-bound suite preflight. The core remains standard-library-only. The optional OIDC module stays at 0.1.0.

# Comfylib releases

## 0.3.0

- Applications now consume the published `suite` package and embedded UI assets
  directly; generated suite copies, adapter templates and synchronization manifests
  are retired.
- Add shared application maintenance commands for service configuration,
  deployment forwarding, release validation, archive scanning, mutations and
  container releases. A small application launcher resolves the exact module pin,
  including with the proxy disabled. Container lifecycle profiles retain both
  existing backup and restore flows.
- Add consumer contracts, offline bootstrap tests, shared container schema,
  property, atomicity and adversarial tests. Existing authentication mutations now
  run centrally across the core and optional protocol module.
- Add `--oidc-version` to coordinated dependency rollouts. Preserve all existing
  exported Go APIs and the core module's standard-library-only dependency contract.

## OIDC companion 0.1.0

The new `github.com/airencracken/comfylib/oidc` module replaces application-local
protocol adapters. It uses CoreOS go-oidc and OAuth2 for signed claims and PKCE,
with bounded discovery requests and cached metadata. Applications keep their
session and permission hooks. It requires Go 1.26.11 and is independently tagged
as `oidc/v0.1.0`. Its integration tests cover explicit identity linking, CSRF,
replay, account status and database atomicity; SQLite is a test dependency.

## 0.2.0

- Add optional app navigation, bounded OpenID Connect flow state, explicit local
  identity bindings, and browser-confirmed discussion returns in `suite`.
- Add shared menu controls, focus styles, form disclosures and saved density
  preferences without changing application palettes or permissions.
- Add coordinated audit, dependency rollout, synchronization and release archive
  vulnerability scanning tools, with adversarial and mutation regression tests.
- Redact service configuration values from errors so secrets do not reach logs.
- Require Go 1.26.11 or newer. Comfylib continues to use only the standard library.

Applications retain account creation, authorization, sessions and database
transactions. OpenID Connect requires an explicitly linked existing local account;
there is no automatic email-based account matching or provisioning. Each app
supplies its own signed-token verification adapter.

All existing exported APIs remain available. Application releases pin the published
module with `GOWORK=off`; no local replacement is needed.
