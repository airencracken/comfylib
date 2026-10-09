// SPDX-License-Identifier: AGPL-3.0-or-later

package memberprofile

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestProfileContractsAndIsolation(t *testing.T) {
	input := Profile{Name: "  A real name  ", Bio: "  A short bio.\nAnother line.  ", Links: []Link{{Label: " My music ", URL: " https://music.example.org/albums?x=1&y=2#latest "}, {}, {URL: "http://example.org/"}}}
	got, err := Normalize(input)
	if err != nil || got.Name != "A real name" || got.Bio != "A short bio.\nAnother line." || len(got.Links) != 2 || got.Links[0].Label != "My music" {
		t.Fatal(got, err)
	}
	got.Links[0].Label = "Changed"
	if input.Links[0].Label != " My music " {
		t.Fatal("normalization changed the caller's links")
	}
	fields := got.LinkFields()
	fields[0].Label = "Other"
	if got.Links[0].Label != "Changed" || fields[4] != (Link{}) {
		t.Fatal("form fields share storage or lack empty slots")
	}
	for _, p := range []Profile{{}, {Name: "<script>alert(1)</script>", Bio: "**Plain text**"}, {Name: strings.Repeat("界", MaxName), Bio: strings.Repeat("é", MaxBio), Links: []Link{{Label: strings.Repeat("é", MaxLabel), URL: "https://example.org/" + strings.Repeat("a", MaxURL-len("https://example.org/"))}}}} {
		first, err := Normalize(p)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Normalize(first)
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Fatal("not idempotent", first, second, err)
		}
	}
	raw, err := json.Marshal(Profile{Name: "Optional", Bio: "Short", Links: []Link{{Label: "Site", URL: "https://example.org/"}}})
	if err != nil || string(raw) != `{"name":"Optional","bio":"Short","links":[{"label":"Site","url":"https://example.org/"}]}` {
		t.Fatal("JSON contract", string(raw), err)
	}
}

func TestRejectsAdversarialProfiles(t *testing.T) {
	for _, p := range []Profile{
		{Name: strings.Repeat("界", MaxName+1)}, {Name: "name\nother"}, {Name: "name\x00"}, {Name: string([]byte{255})},
		{Bio: strings.Repeat("é", MaxBio+1)}, {Bio: "bio\x00"}, {Bio: string([]byte{255})},
		{Links: make([]Link, MaxLinks+1)}, {Links: []Link{{Label: strings.Repeat("é", MaxLabel+1), URL: "https://example.org"}}},
		{Links: []Link{{Label: "bad\nlabel", URL: "https://example.org"}}}, {Links: []Link{{Label: "no address"}}},
	} {
		if got, err := Normalize(p); err == nil || !reflect.DeepEqual(got, Profile{}) {
			t.Fatalf("accepted invalid profile: %#v, %#v, %v", p, got, err)
		}
	}
	for _, address := range []string{"javascript:alert(1)", "data:text/html,bad", "file:///tmp/private", "//example.org/", "/local", "https:", "https:///path", "https://name:secret@example.org/", "https://example.org/%zz", "https://example.org:bad/", "https://example.org/has space", "https://example.org/\x00", "https://example.org\\other", string([]byte{255}), "https://example.org/" + strings.Repeat("a", MaxURL)} {
		if _, err := Normalize(Profile{Links: []Link{{URL: address}}}); err == nil {
			t.Fatalf("accepted unsafe address %q", address)
		}
	}
}

func TestProfileFormContractsAndAdversarialFields(t *testing.T) {
	for _, values := range []url.Values{{}, {"profile_name": {"Optional"}, "profile_bio": {"A bio"}, "profile_link_label": {"Site", ""}, "profile_link_url": {"https://example.org/", ""}}} {
		got, err := ParseForm(values)
		if err != nil || len(got.Links) > 1 {
			t.Fatal(got, err)
		}
	}
	for _, values := range []url.Values{
		{"profile_name": {"one", "two"}}, {"profile_bio": {"one", "two"}},
		{"profile_link_label": {"Only label"}}, {"profile_link_url": {"https://example.org"}},
		{"profile_link_label": make([]string, MaxLinks+1), "profile_link_url": make([]string, MaxLinks+1)},
	} {
		if _, err := ParseForm(values); err == nil {
			t.Fatal("accepted malformed form", values)
		}
	}
	draft, err := ParseForm(url.Values{"profile_name": {"My name"}, "profile_link_label": {"Site"}, "profile_link_url": {"javascript:bad"}})
	if err == nil || draft.Name != "My name" || len(draft.Links) != 1 || draft.Links[0].URL != "javascript:bad" {
		t.Fatal("invalid form lost its editable draft", draft, err)
	}
}

func FuzzProfileNormalization(f *testing.F) {
	f.Add("A name", "A short bio", "Website", "https://example.org/")
	f.Add("", "", "", "javascript:bad")
	f.Fuzz(func(t *testing.T, name, bio, label, address string) {
		p, err := Normalize(Profile{Name: name, Bio: bio, Links: []Link{{Label: label, URL: address}}})
		if err != nil {
			return
		}
		again, err := Normalize(p)
		if err != nil || !reflect.DeepEqual(p, again) {
			t.Fatal("normalization is not idempotent", p, again, err)
		}
		if len(p.Links) > 0 {
			u, err := url.Parse(p.Links[0].URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
				t.Fatal("invalid link accepted", p)
			}
		}
	})
}
