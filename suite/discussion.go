// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pendingDiscussion struct {
	binding [32]byte
	source  string
	expires time.Time
}

// Limits on pending discussion returns held by one DiscussionStore.
const (
	discussionLifetime   = 20 * time.Minute
	discussionCapacity   = 1024
	discussionPerBinding = 16
)

// DiscussionStore holds the pending return addresses of discussion handoffs.
// The zero value is ready to use and is safe for concurrent use. Keep one
// store per process; pending returns live only in memory.
type DiscussionStore struct {
	mu      sync.Mutex
	pending map[string]pendingDiscussion
	now     func() time.Time
}

func (s *DiscussionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Prepare adds a return address, valid for 20 minutes, to a reviewed handoff
// URL. source is the absolute URL of the item being discussed. binding ties
// the return to the signed-in session; an empty binding returns handoff
// unchanged. A store holds at most 1024 pending returns, and 16 per binding.
func (s *DiscussionStore) Prepare(handoff, source, binding string) (string, error) {
	if binding == "" {
		return handoff, nil
	}
	u, e := url.Parse(handoff)
	if e != nil {
		return "", e
	}
	src, e := url.Parse(source)
	if e != nil || !validAppURL(source) {
		return "", errors.New("invalid discussion source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]pendingDiscussion{}
	}
	now := s.clock()
	count := 0
	hash := sha256.Sum256([]byte(binding))
	for k, p := range s.pending {
		if now.After(p.expires) {
			delete(s.pending, k)
		} else if p.binding == hash {
			count++
		}
	}
	if len(s.pending) >= discussionCapacity || count >= discussionPerBinding {
		return "", errors.New("too many pending discussions")
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		return "", e
	}
	token := hex.EncodeToString(random[:])
	s.pending[token] = pendingDiscussion{hash, src.Path, now.Add(discussionLifetime)}
	src.Path = strings.TrimRight(src.Path, "/") + "/discussion-return"
	q := src.Query()
	q.Set("state", token)
	src.RawQuery = q.Encode()
	q = u.Query()
	q.Set("return_to", src.String())
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Check reports whether token is a pending return for sourcePath and the same
// session binding, and whether thread is a topic URL on forum. It does not
// consume the token; call Finish after the app saves the link.
func (s *DiscussionStore) Check(token, sourcePath, binding, thread, forum string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[token]
	if !ok || s.clock().After(p.expires) || p.source != sourcePath || binding == "" || p.binding != sha256.Sum256([]byte(binding)) {
		return false
	}
	return validDiscussionThread(thread, forum)
}

// Finish removes a pending return so it cannot be used again.
func (s *DiscussionStore) Finish(token string) {
	s.mu.Lock()
	delete(s.pending, token)
	s.mu.Unlock()
}

// defaultDiscussions backs the package-level discussion functions.
var defaultDiscussions DiscussionStore

// PrepareDiscussion calls Prepare on the package's default store.
//
// Deprecated: Use a DiscussionStore.
func PrepareDiscussion(handoff, source, binding string) (string, error) {
	return defaultDiscussions.Prepare(handoff, source, binding)
}

// CheckDiscussion calls Check on the package's default store.
//
// Deprecated: Use a DiscussionStore.
func CheckDiscussion(token, sourcePath, binding, thread, forum string) bool {
	return defaultDiscussions.Check(token, sourcePath, binding, thread, forum)
}

// FinishDiscussion calls Finish on the package's default store.
//
// Deprecated: Use a DiscussionStore.
func FinishDiscussion(token string) {
	defaultDiscussions.Finish(token)
}

func validDiscussionThread(thread, forum string) bool {
	base, e := url.Parse(forum)
	if e != nil || !validAppURL(forum) {
		return false
	}
	u, e := url.Parse(thread)
	if e != nil || u.User != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	prefix := strings.TrimRight(base.Path, "/") + "/topics/"
	if !strings.HasPrefix(u.Path, prefix) {
		return false
	}
	id, e := strconv.ParseInt(strings.TrimPrefix(u.Path, prefix), 10, 64)
	return e == nil && id > 0
}

// DiscussionReturn checks a return address from a handoff. It must point to
// source's discussion-return path, carry one state token, and belong to an
// Imvault or Songstead entry in apps (the APPS setting). An empty raw value
// returns "". It never requests the URL.
func DiscussionReturn(raw, source, apps string) (string, error) {
	if raw == "" {
		return "", nil
	}
	fail := errors.New("discussion return is not configured for this source")
	if len(raw) > 4096 {
		return "", fail
	}
	u, e := parseDiscussionReturn(raw, source)
	if e != nil {
		return "", fail
	}
	configured, e := ParseApps(apps)
	if e != nil {
		return "", e
	}
	for _, a := range configured {
		if discussionAppMatches(a, u) {
			return u.String(), nil
		}
	}
	return "", fail
}

// DiscussionResult adds the thread URL to a validated return destination. It
// returns "" when destination is empty or not a URL.
func DiscussionResult(destination, thread string) string {
	u, e := url.Parse(destination)
	if e != nil || destination == "" {
		return ""
	}
	q := u.Query()
	q.Set("thread", thread)
	u.RawQuery = q.Encode()
	return u.String()
}

// ConfiguredDiscussionReturn checks a submitted return field again after the
// post is saved, using the source the field itself names.
func ConfiguredDiscussionReturn(raw, apps string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	u.Path = strings.TrimSuffix(u.Path, "/discussion-return")
	u.RawQuery = ""
	return DiscussionReturn(raw, u.String(), apps)
}

func parseDiscussionReturn(raw, source string) (*url.URL, error) {
	fail := errors.New("invalid discussion return")
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || !strings.HasSuffix(u.Path, "/discussion-return") {
		return nil, fail
	}
	sourceURL, e := url.Parse(source)
	if e != nil || u.Scheme != sourceURL.Scheme || u.Host != sourceURL.Host || u.Path != strings.TrimRight(sourceURL.Path, "/")+"/discussion-return" {
		return nil, fail
	}
	q := u.Query()
	state := q.Get("state")
	if len(q) != 1 || len(q["state"]) != 1 || len(state) != 64 {
		return nil, fail
	}
	if _, e = hex.DecodeString(state); e != nil {
		return nil, fail
	}
	return u, nil
}
func discussionAppMatches(a App, u *url.URL) bool {
	base, _ := url.Parse(a.URL)
	return (a.ID == "imvault" || a.ID == "songstead") && u.Scheme == base.Scheme && u.Host == base.Host && strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+"/")
}
