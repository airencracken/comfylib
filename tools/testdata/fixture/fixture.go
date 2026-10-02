// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fixture is a tiny module for testing tools/mutate.py.
package fixture

// Add is covered by a test, so breaking it must be caught.
func Add(a, b int) int {
	return a + b
}

// Greeting has no test, so changing it must survive.
func Greeting() string {
	return "hello"
}

// Mode is tested only when FIXTURE_MODE is set, to prove a table's env
// reaches the test.
func Mode() string {
	return "on"
}
