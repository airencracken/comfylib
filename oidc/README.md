# OIDC module

`github.com/airencracken/comfylib/oidc` adds OpenID Connect sign-in for
existing local accounts. It is a separate module with its own tags
(`oidc/v0.1.0`) and dependencies (CoreOS go-oidc and OAuth2), so the core
module stays standard-library-only.

Import it as `sharedoidc` and call `FromEnvironment(prefix, baseURL, db, hooks)`.
The prefix selects `SHARED_ISSUER`, `SHARED_CLIENT_ID` and
`SHARED_CLIENT_SECRET`. An empty configuration turns it off; a partial one
fails startup. Members link an issuer and subject to their existing account.
It never matches by email or creates accounts. Your hooks handle sessions,
CSRF, account status and sign-in.

Run `go test -race ./...` and `go vet ./...` here, or `make test-oidc` from the
parent repository.
