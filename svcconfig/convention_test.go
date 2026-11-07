// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"strings"
	"testing"
)

func TestQuotedConventionAgreesBetweenServiceManagers(t *testing.T) {
	for _, value := range []string{"", "true", "false", "7", "24h", "/var/lib/app", "127.0.0.1:8084", "https://app.example.org", "Name <mail@example.org>", "literal $value and `text`", `a"b\c`} {
		line := "APP_VALUE=" + doubleQuote(value) + "\n"
		for _, shell := range []bool{true, false} {
			got, found, err := scanAssignments(strings.NewReader(line), "fixture", "APP_VALUE", shell)
			if err != nil || !found || got != value {
				t.Fatalf("shell=%v: quoted convention changed a value (found=%v, err=%v)", shell, found, err)
			}
		}
	}
}

func TestLegacyConfigurationRemainsAccepted(t *testing.T) {
	for _, raw := range []string{"plain-value", "'plain-value'", `"plain-value"`} {
		for _, shell := range []bool{true, false} {
			got, found, err := scanAssignments(strings.NewReader("APP_VALUE="+raw+"\n"), "fixture", "APP_VALUE", shell)
			if err != nil || !found || got != "plain-value" {
				t.Fatalf("legacy configuration no longer accepted: shell=%v, found=%v, err=%v", shell, found, err)
			}
		}
	}
}
