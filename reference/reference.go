// SPDX-License-Identifier: AGPL-3.0-or-later

// Package reference provides browser-only Comfyware discussion handoffs.
// A handoff prepares a draft; it never posts or fetches a destination.
package reference

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// URL validates an absolute, credential-free web address without fetching it.
func URL(raw string) (*url.URL, error) {
	if len(raw) > 4096 || !utf8.ValidString(raw) || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, errors.New("use a web address without whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || strings.Contains(u.Host, "\\") {
		return nil, errors.New("use an absolute HTTP(S) address without credentials")
	}
	if strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid web address")
	}
	if u.Port() != "" {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid web port")
		}
	}
	return u, nil
}

// Draft contains deliberately shared text and a link back to its source.
// Do not put private notes, credentials, or private album metadata in a draft.
type Draft struct {
	Source string
	Title  string
	Body   string
}

func (d Draft) Validate() error {
	if _, err := URL(d.Source); err != nil {
		return err
	}
	for _, field := range []struct {
		text string
		max  int
	}{{d.Title, 160}, {d.Body, 4000}} {
		if !utf8.ValidString(field.text) || utf8.RuneCountInString(field.text) > field.max || strings.IndexFunc(field.text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
			return errors.New("discussion draft is too long or contains invalid text")
		}
	}
	return nil
}

// Handoff targets Witmoot's share page. The destination is operator configured;
// it may include a deployment path prefix, but never a query or fragment.
func Handoff(base string, d Draft) (string, error) {
	u, err := URL(base)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("discussion server needs a base URL")
	}
	if err = d.Validate(); err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/share"
	u.RawQuery = url.Values{"source": {d.Source}, "title": {d.Title}, "body": {d.Body}}.Encode()
	return u.String(), nil
}

// Read validates untrusted handoff query fields, including duplicates.
func Read(q url.Values) (Draft, error) {
	d := Draft{}
	for key, target := range map[string]*string{"source": &d.Source, "title": &d.Title, "body": &d.Body} {
		if len(q[key]) != 1 {
			return d, errors.New("invalid discussion handoff")
		}
		*target = q.Get(key)
	}
	return d, d.Validate()
}
