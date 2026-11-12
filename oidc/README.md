# Optional OIDC adapter

`github.com/airencracken/comfylib/oidc` is a separately versioned module. It uses
CoreOS go-oidc and OAuth2 for discovery, signature verification and PKCE. The
parent Comfylib module continues to use only the Go standard library.

Import this module as `sharedoidc` and call `FromEnvironment(prefix, baseURL, db,
hooks)`. The prefix selects `SHARED_ISSUER`, `SHARED_CLIENT_ID` and
`SHARED_CLIENT_SECRET`. An entirely empty configuration disables the integration;
a partial configuration fails startup. Existing local accounts must explicitly
link their issuer and subject. Email matching and automatic account creation are
not supported. Applications supply session, CSRF, account status and sign-in hooks.

The module is released with tags such as `oidc/v0.1.0`; its Go requirement is
`github.com/airencracken/comfylib/oidc v0.1.0`. Tests cover signed claims, nonce,
PKCE, discovery caching, explicit linking, replay, session binding, disabled
accounts and failed database writes. SQLite is used only by integration tests.

Run `go test -race ./...` and `go vet ./...` here. From the parent repository,
`make test-oidc` also verifies mutations against an isolated copy of both modules.
