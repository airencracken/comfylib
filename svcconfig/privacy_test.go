// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"strings"
	"testing"
)

func TestConfigurationErrorsDoNotDiscloseValues(t *testing.T) {
	t.Setenv("APP_SMTP_PASSWORD", "")
	const secret = "private-credential-marker"
	for _, raw := range []string{secret + "$(command)", secret + " trailing", `"` + secret + "$EXPANSION" + `"`} {
		t.Run(raw, func(t *testing.T) {
			p := openRCPaths(t, "APP_SMTP_PASSWORD="+raw+"\n")
			_, err := p.Settings("APP_SMTP_PASSWORD")
			if err == nil {
				t.Fatal("dynamic or malformed value accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("configuration error disclosed the credential")
			}
			if !strings.Contains(err.Error(), "APP_SMTP_PASSWORD") || !strings.Contains(err.Error(), p.OpenRCConfig+":1") {
				t.Fatal("configuration error lost its setting name or source location")
			}
		})
	}
	p := testPaths()
	p.OpenRCInstalled = true
	p.OpenRCConfig = writeTestFile(t, t.TempDir()+"/app.conf", "APP_SMTP_PASSWORD="+secret+"\n")
	p.SystemdUnit = writeTestFile(t, t.TempDir()+"/app.service", "[Service]\nEnvironment=APP_SMTP_PASSWORD=different\n")
	if _, err := p.Settings("APP_SMTP_PASSWORD"); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("conflicting service settings must be reported without their values")
	}
}
