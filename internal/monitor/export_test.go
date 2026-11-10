package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configForTest() SiteConfig {
	return SiteConfig{Repository: "VpSanta33/SRC-Monitoring", Workflow: "pages.yml", Schedule: "17,47 * * * *", Timezone: "UTC", Enabled: map[string]bool{"hackerone": true, "bugcrowd": true}}
}
func TestCheckpointRoundTripAndStaticPublication(t *testing.T) {
	s := testStore(t)
	raw := fixture(asset("a.example"), asset("b.example"), asset("c.example"), asset("d.example"))
	apply(t, s, raw)
	changed := asset("b.example")
	changed["scope"] = "out"
	next := fixture(asset("a.example"), changed, asset("d.example"), asset("e.example"))
	apply(t, s, next)
	// Local legacy preferences and ETags must never enter the public website.
	s.DB.Exec("UPDATE records SET watched=1")
	s.DB.Exec("UPDATE changes SET seen=1")
	checkpoint := filepath.Join(t.TempDir(), "state")
	if e := s.SaveCheckpoint(checkpoint); e != nil {
		t.Fatal(e)
	}
	originalID, _ := s.DatasetID()
	restored := testStore(t)
	if e := restored.RestoreCheckpoint(checkpoint); e != nil {
		t.Fatal(e)
	}
	id, _ := restored.DatasetID()
	if originalID != id {
		t.Fatal("namespace changed across runs")
	}
	if count := apply(t, restored, next); count != 0 {
		t.Fatal("restored baseline generated duplicate changes")
	}
	var changes int
	restored.DB.QueryRow("SELECT count(*) FROM changes").Scan(&changes)
	if changes != 3 {
		t.Fatal("history lost", changes)
	}
	var watches int
	restored.DB.QueryRow("SELECT sum(watched) FROM records").Scan(&watches)
	if watches != 0 {
		t.Fatal("browser preference copied to shared state")
	}
	out := t.TempDir()
	if e := restored.ExportSite(out, configForTest()); e != nil {
		t.Fatal(e)
	}
	rawManifest, e := os.ReadFile(filepath.Join(out, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var manifest PublishedManifest
	if e = json.Unmarshal(rawManifest, &manifest); e != nil {
		t.Fatal(e)
	}
	if manifest.DatasetID != id || manifest.HistoryTotal != 3 {
		t.Fatal("invalid publication metadata")
	}
	for _, name := range manifest.Files {
		if _, e = os.Stat(filepath.Join(out, name)); e != nil {
			t.Fatal(e)
		}
	}
	programs, e := os.ReadFile(filepath.Join(out, manifest.Files["programs"]))
	if e != nil {
		t.Fatal(e)
	}
	for _, forbidden := range []string{`"watched"`, `"program_watched"`, `"etag"`, `"seen"`} {
		if strings.Contains(string(programs), forbidden) {
			t.Fatalf("leaked private state field %s", forbidden)
		}
	}
	assets, e := os.ReadFile(filepath.Join(out, manifest.Files["assets-hackerone"]))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(assets), `"metadata"`) {
		t.Fatal("asset index should defer large metadata")
	}
	var list []map[string]any
	if e = json.Unmarshal(programs, &list); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(out, list[0]["detail_file"].(string))); e != nil {
		t.Fatal(e)
	}
}
func TestCorruptCheckpointCannotResetBaseline(t *testing.T) {
	s := testStore(t)
	apply(t, s, fixture(asset("a.example")))
	dir := filepath.Join(t.TempDir(), "state")
	if e := s.SaveCheckpoint(dir); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "records-000000.jsonl"), []byte("{}\n"), 0644); e != nil {
		t.Fatal(e)
	}
	restored := testStore(t)
	if e := restored.RestoreCheckpoint(dir); e == nil || !strings.Contains(e.Error(), "checksum") {
		t.Fatal("corruption accepted", e)
	}
	var n int
	restored.DB.QueryRow("SELECT count(*) FROM records").Scan(&n)
	if n != 0 {
		t.Fatal("partial restore committed")
	}
}
func TestFailedSourcePreservesBaselineAndExportsError(t *testing.T) {
	s := testStore(t)
	apply(t, s, fixture(asset("a.example")))
	s.DB.Exec("UPDATE sources SET enabled=0 WHERE platform='bugcrowd'")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	m := NewSyncer(s)
	defer m.Close()
	m.Client = server.Client()
	m.URLs = map[string]string{"hackerone": server.URL}
	if e := m.Collect(); e == nil {
		t.Fatal("failure not returned to CI")
	}
	out := t.TempDir()
	if e := s.ExportSite(out, configForTest()); e != nil {
		t.Fatal(e)
	}
	sources, _ := s.Sources()
	for _, source := range sources {
		if source.Platform == "hackerone" && (source.Status != "error" || source.ProgramCount != 1 || source.LastSuccess == "") {
			t.Fatal("last valid dataset lost", source)
		}
	}
}
