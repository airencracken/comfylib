// SPDX-License-Identifier: AGPL-3.0-or-later

package proxytest_test

import (
	"os"
	"testing"

	"github.com/airencracken/comfylib/proxyconfig"
	"github.com/airencracken/comfylib/proxyconfig/proxytest"
)

// An app's contrib/proxy test, behind the proxyintegration build tag, is one
// call with the app's own Spec.
func ExampleReverseProxies() {
	_ = func(t *testing.T) {
		proxytest.ReverseProxies(t, proxyconfig.Spec{
			App:              "witmoot",
			ExampleDomain:    "board.example.org",
			DefaultUpstream:  "127.0.0.1:8082",
			ProxyEnvironment: "WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128",
			Examples:         os.DirFS("contrib"),
		})
	}
}
