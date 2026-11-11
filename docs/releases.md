# Comfylib releases

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
