// SPDX-License-Identifier: AGPL-3.0-or-later

package smtp_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/airencracken/comfylib/smtp"
)

func Example() {
	// Settings as an app reads them; the names are the app's own.
	mode, err := smtp.ParseTLSMode("")
	if err != nil {
		fmt.Println("APP_SMTP_TLS:", err)
		return
	}
	from, err := smtp.ParseFrom("Board <no-reply@board.example.org>")
	if err != nil {
		fmt.Println("APP_SMTP_FROM:", err)
		return
	}
	var sender smtp.Sender = smtp.Disabled{}
	relay, err := smtp.New(smtp.Config{Host: "smtp.example.org", Port: 587, From: from, Mode: mode, Timeout: 10 * time.Second})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(mode, from, relay.Enabled())

	// Without a relay, sending is refused rather than logged.
	err = sender.Send(context.Background(), smtp.Message{To: "alice@example.org", Subject: "Reset", Body: "secret link"})
	fmt.Println(sender.Enabled(), errors.Is(err, smtp.ErrDisabled))
	// Output:
	// starttls "Board" <no-reply@board.example.org> true
	// false true
}

func ExampleParseFrom() {
	for _, value := range []string{"Café Crew <no-reply@example.org>", "no-reply@example.org\r\nBcc: someone@example.org"} {
		canonical, err := smtp.ParseFrom(value)
		fmt.Printf("%q %v\n", canonical, err)
	}
	// Output:
	// "=?utf-8?q?Caf=C3=A9_Crew?= <no-reply@example.org>" <nil>
	// "" the sender must be on one line
}
