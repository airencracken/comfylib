// SPDX-License-Identifier: AGPL-3.0-or-later

// Package memberprofile validates optional, plain-text member biographies and
// links. Applications own storage, account identity and profile visibility.
package memberprofile

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxName  = 80
	MaxBio   = 1000
	MaxLinks = 5
	MaxLabel = 80
	MaxURL   = 2048
)

// Link is a member-supplied HTTP or HTTPS destination. Label may be empty.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Profile contains only information a member deliberately publishes. Name is
// independent of the username used to sign in. All fields are optional.
type Profile struct {
	Name  string `json:"name"`
	Bio   string `json:"bio"`
	Links []Link `json:"links"`
}

// Normalize trims outer whitespace and removes empty link slots. It returns a
// fresh profile without changing its input. Invalid profiles return a zero value.
// Nothing is fetched, and text remains plain text for the caller to escape.
func Normalize(p Profile) (Profile, error) {
	out := Profile{Name: strings.TrimSpace(p.Name), Bio: strings.TrimSpace(p.Bio), Links: []Link{}}
	if !validText(p.Name, MaxName, false) {
		return Profile{}, errors.New("name must be plain text, up to 80 characters")
	}
	if !validText(p.Bio, MaxBio, true) {
		return Profile{}, errors.New("bio must be plain text, up to 1000 characters")
	}
	if len(p.Links) > MaxLinks {
		return Profile{}, errors.New("a profile may have up to five links")
	}
	for _, link := range p.Links {
		clean, err := normalizeLink(link)
		if err != nil {
			return Profile{}, err
		}
		if clean.URL != "" {
			out.Links = append(out.Links, clean)
		}
	}
	return out, nil
}

func validText(value string, limit int, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && (!multiline || (r != '\n' && r != '\r' && r != '\t')) {
			return false
		}
	}
	return true
}

func normalizeLink(link Link) (Link, error) {
	if !validText(link.Label, MaxLabel, false) || len(link.URL) > MaxURL || !utf8.ValidString(link.URL) {
		return Link{}, errors.New("link labels may have up to 80 characters and addresses up to 2048 bytes")
	}
	link.Label, link.URL = strings.TrimSpace(link.Label), strings.TrimSpace(link.URL)
	if link.Label == "" && link.URL == "" {
		return link, nil
	}
	parsed, err := url.Parse(link.URL)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(link.URL, "\\") || strings.ContainsFunc(link.URL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return Link{}, errors.New("use a complete http:// or https:// link without credentials or spaces")
	}
	return link, nil
}

// ParseForm reads the shared profile field names. On validation failure it
// returns the submitted draft for escaped form rendering, never for storage.
// Duplicate scalar fields and mismatched link arrays are rejected.
func ParseForm(values url.Values) (Profile, error) {
	p := Profile{Name: values.Get("profile_name"), Bio: values.Get("profile_bio"), Links: []Link{}}
	labels, addresses := values["profile_link_label"], values["profile_link_url"]
	if len(values["profile_name"]) > 1 || len(values["profile_bio"]) > 1 || len(labels) != len(addresses) || len(addresses) > MaxLinks {
		return p, errors.New("submit one name, one bio and up to five matching link fields")
	}
	for i, address := range addresses {
		p.Links = append(p.Links, Link{Label: labels[i], URL: address})
	}
	clean, err := Normalize(p)
	if err != nil {
		return p, err
	}
	return clean, nil
}

// LinkFields supplies five independent form slots, retaining saved link order.
func (p Profile) LinkFields() [MaxLinks]Link {
	var fields [MaxLinks]Link
	copy(fields[:], p.Links)
	return fields
}
