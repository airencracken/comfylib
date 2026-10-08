// SPDX-License-Identifier: AGPL-3.0-or-later

package reference

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/quick"
)

func TestHandoffContract(t *testing.T) {
	d := Draft{Source: "https://music.example/recommendations/2", Title: "Music & friends", Body: "An album\nhttps://provider.example/album/1"}
	raw, err := Handoff("https://board.example/prefix/", d)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Path != "/prefix/share" {
		t.Fatal(u)
	}
	got, err := Read(u.Query())
	if err != nil || got != d {
		t.Fatalf("%+v %v", got, err)
	}
	q := u.Query()
	q.Add("source", d.Source)
	if _, err := Read(q); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestAdversarialURLs(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "//host", "https://host:999999/", "https://host/%0Asecret", "https://user:secret@host/", "https://host/\nsecret", "https://host/a b", "https://host\\evil/", strings.Repeat("a", 4097)} {
		if _, err := URL(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestURLPortBoundaries(t *testing.T) {
	for _, port := range []string{"0", "65536", "999999", "-1", "words"} {
		if _, err := URL("https://host:" + port + "/"); err == nil {
			t.Fatalf("accepted invalid port %q", port)
		}
	}
	for _, raw := range []string{"https://host/", "https://host:1/", "https://host:65535/", "http://[::1]:8083/prefix"} {
		if _, err := URL(raw); err != nil {
			t.Fatalf("rejected valid URL %q: %v", raw, err)
		}
	}
	if err := quick.Check(func(port uint16) bool {
		_, err := URL("https://host:" + strconv.Itoa(int(port)) + "/")
		return (err == nil) == (port != 0)
	}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestHandoffRejectsInvalidDraftAndBase(t *testing.T) {
	for _, base := range []string{"https://host/?secret=x", "https://host/#x", "/relative"} {
		if _, err := Handoff(base, Draft{Source: "https://source"}); err == nil {
			t.Fatal(base)
		}
	}
	if _, err := Handoff("https://host", Draft{Source: "https://source", Title: strings.Repeat("x", 161)}); err == nil {
		t.Fatal("long title")
	}
}
func FuzzDraftRoundTrip(f *testing.F) {
	f.Add("A title", "Some words")
	f.Fuzz(func(t *testing.T, title, body string) {
		d := Draft{Source: "https://source.example/item", Title: title, Body: body}
		raw, err := Handoff("https://board.example", d)
		if err != nil {
			return
		}
		u, _ := url.Parse(raw)
		got, err := Read(u.Query())
		if err != nil || got != d {
			t.Fatalf("round trip %+v %v", got, err)
		}
	})
}
