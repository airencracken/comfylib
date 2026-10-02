// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build proxyintegration

package proxytest_test

import (
	"os"
	"testing"

	"github.com/airencracken/comfylib/proxyconfig"
	"github.com/airencracken/comfylib/proxyconfig/proxytest"
)

// The examples under proxyconfig/testdata are witmoot's, renamed, so this runs
// the same checks the apps run against their own.
func TestReverseProxies(t *testing.T) {
	proxytest.ReverseProxies(t, proxyconfig.Spec{
		App:              "exampleapp",
		ExampleDomain:    "app.example.org",
		DefaultUpstream:  "127.0.0.1:8082",
		ProxyEnvironment: "EXAMPLEAPP_TRUSTED_PROXIES=127.0.0.1/32,::1/128",
		Examples:         os.DirFS("../testdata"),
	})
}
