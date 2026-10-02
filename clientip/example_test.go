// SPDX-License-Identifier: AGPL-3.0-or-later

package clientip_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/airencracken/comfylib/clientip"
)

func Example() {
	// Usually read from a setting such as WITMOOT_TRUSTED_PROXIES.
	trusted, err := clientip.ParseTrusted("127.0.0.1/32, ::1/128", "APP_TRUSTED_PROXIES")
	if err != nil {
		fmt.Println(err)
		return
	}
	res := clientip.Resolver{Trusted: trusted}

	// A request relayed by the local proxy, whose client tried to claim
	// another address by sending its own X-Forwarded-For.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:41000"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 2001:db8:1:2::77")
	r.Header.Set("X-Forwarded-Proto", "https")

	client := res.Client(r)
	fmt.Println(client)
	fmt.Println(clientip.NetworkKey(client))
	fmt.Println(res.ForwardedHTTPS(r))

	// The same headers from anywhere else are ignored.
	r.RemoteAddr = "198.51.100.9:41000"
	fmt.Println(res.Client(r), res.ForwardedHTTPS(r))
	// Output:
	// 2001:db8:1:2::77
	// 2001:db8:1:2::/64
	// true
	// 198.51.100.9 false
}

func ExampleParseTrusted() {
	_, err := clientip.ParseTrusted("localhost", "APP_TRUSTED_PROXIES")
	fmt.Println(err)
	// Output: APP_TRUSTED_PROXIES: invalid proxy IP or CIDR "localhost"
}
