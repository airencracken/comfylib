// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiscussionRoundTrip(t *testing.T) {
	source := "https://music.example/recommendations/8"
	forum := "https://board.example"
	thread := forum + "/topics/42"
	link, e := PrepareDiscussion(forum+"/share?source="+url.QueryEscape(source), source, "local-session")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(link)
	raw := u.Query().Get("return_to")
	destination, e := DiscussionReturn(raw, source, `[{"id":"songstead","url":"https://music.example"}]`)
	if e != nil {
		t.Fatal(e)
	}
	result, _ := url.Parse(DiscussionResult(destination, thread))
	token := result.Query().Get("state")
	if !CheckDiscussion(token, "/recommendations/8", "local-session", thread, forum) {
		t.Fatal("valid return rejected")
	}
	for _, v := range []struct{ path, binding, thread string }{{"/recommendations/9", "local-session", thread}, {"/recommendations/8", "other-session", thread}, {"/recommendations/8", "local-session", "https://evil.example/topics/42"}, {"/recommendations/8", "local-session", forum + "/topics/42?next=evil"}, {"/recommendations/8", "local-session", forum + "/topics/-1"}} {
		if CheckDiscussion(token, v.path, v.binding, v.thread, forum) {
			t.Fatal("forged return accepted", v)
		}
	}
	for _, v := range []string{"", `[{"id":"imvault","url":"https://other.example"}]`} {
		if _, e := DiscussionReturn(raw, source, v); e == nil {
			t.Fatal("unconfigured destination accepted")
		}
	}
	if _, e := DiscussionReturn(strings.Replace(raw, "/recommendations/8/", "/recommendations/9/", 1), source, `[{"id":"songstead","url":"https://music.example"}]`); e == nil {
		t.Fatal("changed entity accepted")
	}
	FinishDiscussion(token)
	if CheckDiscussion(token, "/recommendations/8", "local-session", thread, forum) {
		t.Fatal("return replay accepted")
	}
}
func FuzzDiscussionReturn(f *testing.F) {
	f.Add("https://bad.example/discussion-return", "https://music.example/recommendations/1")
	f.Fuzz(func(t *testing.T, raw, source string) {
		_, _ = DiscussionReturn(raw, source, `[{"id":"songstead","url":"https://music.example"}]`)
	})
}

func prepareToken(t *testing.T, store *DiscussionStore, source, binding string) string {
	t.Helper()
	link, e := store.Prepare("https://board.example/share", source, binding)
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(link)
	if e != nil {
		t.Fatal(e)
	}
	back, e := url.Parse(u.Query().Get("return_to"))
	if e != nil {
		t.Fatal(e)
	}
	return back.Query().Get("state")
}

func TestDiscussionStoresAreIndependent(t *testing.T) {
	var first, second DiscussionStore
	source, thread, forum := "https://music.example/recommendations/8", "https://board.example/topics/1", "https://board.example"
	token := prepareToken(t, &first, source, "session")
	if !first.Check(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("own store rejected its token")
	}
	if second.Check(token, "/recommendations/8", "session", thread, forum) || CheckDiscussion(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("token leaked between stores")
	}
	second.Finish(token) // finishing in another store must not consume it
	if !first.Check(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("another store consumed the token")
	}
	first.Finish(token)
	if first.Check(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("finished token accepted")
	}
}

func TestDiscussionStoreExpiryAndLimits(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	store := &DiscussionStore{now: func() time.Time { return now }}
	source, thread, forum := "https://music.example/recommendations/8", "https://board.example/topics/1", "https://board.example"
	token := prepareToken(t, store, source, "session")
	now = now.Add(discussionLifetime)
	if !store.Check(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("token expired before its lifetime")
	}
	now = now.Add(time.Nanosecond)
	if store.Check(token, "/recommendations/8", "session", thread, forum) {
		t.Fatal("expired token accepted")
	}
	for i := 0; i < discussionPerBinding; i++ {
		prepareToken(t, store, source, "busy")
	}
	if _, e := store.Prepare("https://board.example/share", source, "busy"); e == nil {
		t.Fatal("per-session limit not enforced")
	}
	prepareToken(t, store, source, "other")
	if len(store.pending) != discussionPerBinding+1 {
		t.Fatal("expired entries were not pruned", len(store.pending))
	}
	if link, e := store.Prepare("https://board.example/share", source, ""); e != nil || link != "https://board.example/share" {
		t.Fatal("unbound handoff changed", link, e)
	}
	for _, bad := range []string{"javascript:alert(1)", "/relative", "https://user:pass@music.example/x", ""} {
		if _, e := store.Prepare("https://board.example/share", bad, "session"); e == nil {
			t.Fatal("bad source accepted", bad)
		}
	}
}

func TestDiscussionStoreConcurrentUse(t *testing.T) {
	var store DiscussionStore
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Go(func() {
			link, e := store.Prepare("https://board.example/share", "https://music.example/recommendations/8", fmt.Sprint("s", i%8))
			if e != nil {
				t.Error(e)
				return
			}
			u, _ := url.Parse(link)
			back, _ := url.Parse(u.Query().Get("return_to"))
			store.Finish(back.Query().Get("state"))
		})
	}
	group.Wait()
	if len(store.pending) != 0 {
		t.Fatal("pending returns left behind", len(store.pending))
	}
}

func TestDiscussionPageEscapesAndMatchesTheDeprecatedRenderer(t *testing.T) {
	page := DiscussionPage{Action: "/save", Source: "/items/1", Thread: `https://board.example/topics/1"><script>x()</script>`, State: "s", CSRFField: "csrf", CSRFToken: "<t>", Stylesheet: "/static/comfyware/components.css"}
	w := httptest.NewRecorder()
	if e := RenderDiscussionPage(w, page); e != nil {
		t.Fatal(e)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, `name="csrf" value="&lt;t&gt;"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe or incomplete page", body)
	}
	old := httptest.NewRecorder()
	RenderDiscussion(old, page.Action, page.Source, page.Thread, page.State, page.CSRFField, page.CSRFToken, page.Stylesheet)
	if old.Body.String() != body || old.Header().Get("Content-Type") != w.Header().Get("Content-Type") {
		t.Fatal("deprecated renderer differs")
	}
}

func FuzzDiscussionStoreCheck(f *testing.F) {
	f.Add("token", "/a", "b", "https://board.example/topics/1", "https://board.example")
	var store DiscussionStore
	f.Fuzz(func(t *testing.T, token, path, binding, thread, forum string) {
		if store.Check(token, path, binding, thread, forum) {
			t.Fatal("empty store accepted a return")
		}
	})
}
