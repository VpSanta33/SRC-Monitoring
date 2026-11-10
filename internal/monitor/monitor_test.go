package monitor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(assets ...map[string]any) []byte {
	in, out := []any{}, []any{}
	for _, a := range assets {
		if a["scope"] == "out" {
			delete(a, "scope")
			out = append(out, a)
		} else {
			in = append(in, a)
		}
	}
	return []byte(encode([]any{map[string]any{"name": "Example", "handle": "example", "url": "https://hackerone.com/example", "offers_bounties": true, "targets": map[string]any{"in_scope": in, "out_of_scope": out}}}))
}
func asset(v string) map[string]any {
	return map[string]any{"asset_identifier": v, "asset_type": "URL", "instruction": "original"}
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := OpenStore(filepath.Join(t.TempDir(), "monitor.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func apply(t *testing.T, s *Store, raw []byte) int {
	t.Helper()
	id, e := s.BeginRun("hackerone")
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.ApplySnapshot("hackerone", raw, id, "etag")
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestSnapshotLifecycle(t *testing.T) {
	s := testStore(t)
	baseline := fixture(asset("a.example"), asset("b.example"), asset("c.example"), asset("d.example"))
	if n := apply(t, s, baseline); n != 0 {
		t.Fatalf("baseline generated %d changes", n)
	}
	if n := apply(t, s, baseline); n != 0 {
		t.Fatalf("identical snapshot generated %d changes", n)
	}
	changed := asset("b.example")
	changed["scope"] = "out"
	changed["instruction"] = "now excluded"
	if n := apply(t, s, fixture(asset("a.example"), changed, asset("d.example"), asset("e.example"))); n != 3 {
		t.Fatalf("want add,modify,remove; got %d", n)
	}
	items, e := s.List("changes", url.Values{}, false)
	if e != nil {
		t.Fatal(e)
	}
	kinds := map[string]bool{}
	for _, r := range items.Items {
		kinds[r["kind"].(string)] = true
		if r["kind"] == "modified" {
			if r["before_data"].(map[string]any)["scope"] != "in_scope" || r["after_data"].(map[string]any)["scope"] != "out_of_scope" {
				t.Fatal("scope transition lost")
			}
		}
	}
	for _, k := range []string{"added", "modified", "removed"} {
		if !kinds[k] {
			t.Fatal("missing", k)
		}
	}
	id, _ := s.BeginRun("hackerone")
	if _, e = s.ApplySnapshot("hackerone", fixture(asset("a.example")), id, ""); e == nil {
		t.Fatal("mass deletion accepted")
	}
	if e = s.FailRun("hackerone", id, e); e != nil {
		t.Fatal(e)
	}
	list, e := s.List("assets", url.Values{}, false)
	if e != nil || list.Total != 4 {
		t.Fatalf("failed sync changed assets: %+v %v", list, e)
	}
	count := 0
	if e = s.DB.QueryRow("SELECT count(*) FROM snapshots").Scan(&count); e != nil || count != 2 {
		t.Fatalf("raw snapshots count %d: %v", count, e)
	}
	sources, _ := s.Sources()
	for _, source := range sources {
		if source.Platform == "hackerone" && (source.Status != "error" || source.LastSuccess == "") {
			t.Fatal("lost last good synchronization state")
		}
	}
}
func TestNormalizeStrictAndConflict(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `{}`, `[{"name":"test","handle":"test","url":"https://hackerone.com/test","targets":{"in_scope":[]}}]`} {
		if _, e := Normalize("hackerone", []byte(raw)); e == nil {
			t.Fatalf("accepted invalid snapshot %s", raw)
		}
	}
	raw := []byte(`[{"name":"Acme","url":"https://bugcrowd.com/engagements/acme","targets":{"in_scope":[{"target":"*.example.com","type":"website","name":"A"},{"target":"*.example.com","type":"website","name":"A"}],"out_of_scope":[{"target":"*.example.com","type":"website","name":"B"}]}}]`)
	snap, e := Normalize("bugcrowd", raw)
	if e != nil {
		t.Fatal(e)
	}
	if snap.Assets != 1 || snap.Programs != 1 {
		t.Fatal("dedup failed")
	}
	for _, r := range snap.Records {
		if r.Entity == "asset" && (!r.Conflict || r.Scope != "out_of_scope" || len(r.Metadata) != 2) {
			t.Fatalf("conflict lost: %+v", r)
		}
	}
}
func TestWatchSettingsRestartAndFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	s, e := OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	apply(t, s, fixture(asset("100%_literal.example"), asset("other.example")))
	if _, e = s.DB.Exec("UPDATE records SET watched=1 WHERE id='hackerone:example'"); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSettings(15, map[string]bool{"hackerone": true, "bugcrowd": false}); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if interval, _ := s.Interval(); interval != 15 {
		t.Fatal("settings lost")
	}
	list, e := s.List("assets", url.Values{"q": {"%_"}, "watched": {"1"}}, false)
	if e != nil || list.Total != 1 {
		t.Fatalf("literal wildcard or watch filtering failed: %+v %v", list, e)
	}
	list, e = s.List("assets", url.Values{"q": {"' OR 1=1 --"}}, false)
	if e != nil || list.Total != 0 {
		t.Fatalf("query injection: %+v %v", list, e)
	}
	if _, e = s.List("assets", url.Values{"page": {"-1"}}, false); e == nil {
		t.Fatal("invalid page accepted")
	}
	if e = s.SaveSettings(1, map[string]bool{"hackerone": true, "bugcrowd": true}); e == nil {
		t.Fatal("invalid interval accepted")
	}
}
func TestConditionalFetch(t *testing.T) {
	s := testStore(t)
	m := NewSyncer(s)
	defer m.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "valid" {
			w.WriteHeader(304)
			return
		}
		io.WriteString(w, string(fixture(asset("a.example"))))
	}))
	defer server.Close()
	m.URLs = map[string]string{"hackerone": server.URL}
	m.Client = server.Client()
	if raw, tag, err := m.Fetch("hackerone", "valid"); err != nil || raw != nil || tag != "valid" {
		t.Fatalf("conditional request failed: %s %v", tag, err)
	}
}
func TestShutdownCancelsFetch(t *testing.T) {
	s := testStore(t)
	m := NewSyncer(s)
	m.cancel()
	_, _, e := m.Fetch("hackerone", "")
	if e == nil || !strings.Contains(e.Error(), context.Canceled.Error()) {
		t.Fatal("fetch ignores cancellation", e)
	}
	m.Close()
}
