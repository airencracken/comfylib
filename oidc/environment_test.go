// SPDX-License-Identifier: AGPL-3.0-or-later
package oidc

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/airencracken/comfylib/suite"
)

// Compile the public adapter contract separately from the standard-library core.
var _ func(string, string, *sql.DB, suite.AuthHooks) (*suite.Auth, error) = FromEnvironment

func TestFromEnvironmentDisabledPartialAndIsolatedPrefixes(t *testing.T) {
	for _, prefix := range []string{"FOLKWEAVE_", "IMVAULT_", "SONGSTEAD_", "TIDINGS_", "WITMOOT_"} {
		t.Run(prefix, func(t *testing.T) {
			for _, suffix := range []string{"SHARED_ISSUER", "SHARED_CLIENT_ID", "SHARED_CLIENT_SECRET"} {
				t.Setenv(prefix+suffix, "")
			}
			t.Setenv("OTHER_SHARED_ISSUER", "https://identity.example.org")
			auth, err := FromEnvironment(prefix, "https://app.example.org", nil, suite.AuthHooks{})
			if err != nil || auth != nil {
				t.Fatalf("disabled integration: %v %v", auth, err)
			}
			t.Setenv(prefix+"SHARED_CLIENT_SECRET", "private-fixture")
			auth, err = FromEnvironment(prefix, "https://app.example.org", nil, suite.AuthHooks{})
			if err == nil || auth != nil || strings.Contains(err.Error(), "private-fixture") {
				t.Fatalf("partial configuration accepted or exposed credentials: %v %v", auth, err)
			}
		})
	}
}

func TestDiscoveryIsCachedAndFailedDiscoveryCanRetry(t *testing.T) {
	var calls atomic.Int32
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/authorize","token_endpoint":"` + issuer + `/token","jwks_uri":"` + issuer + `/keys","id_token_signing_alg_values_supported":["RS256"]}`))
	}))
	defer server.Close()
	issuer = server.URL
	p := &provider{config: suite.AuthConfig{Issuer: issuer, ClientID: "client"}, client: server.Client()}
	if _, err := p.discovery(context.Background()); err == nil {
		t.Fatal("failed discovery accepted")
	}
	for range 3 {
		if _, err := p.discovery(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("discovery was not cached: %d requests", calls.Load())
	}
}
