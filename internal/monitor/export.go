package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SiteConfig struct {
	Repository string          `json:"repository"`
	Workflow   string          `json:"workflow"`
	Schedule   string          `json:"schedule"`
	Timezone   string          `json:"timezone"`
	Enabled    map[string]bool `json:"enabled"`
}

func LoadConfig(path string) (SiteConfig, error) {
	var c SiteConfig
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if c.Repository == "" || c.Workflow == "" || c.Schedule == "" || c.Timezone != "UTC" || len(c.Enabled) != 2 {
		return c, fmt.Errorf("invalid monitor config")
	}
	for _, p := range Platforms {
		if _, ok := c.Enabled[p]; !ok {
			return c, fmt.Errorf("missing platform %s", p)
		}
	}
	return c, nil
}

type PublishedManifest struct {
	Schema       int               `json:"schema"`
	DatasetID    string            `json:"dataset_id"`
	Version      string            `json:"version"`
	GeneratedAt  string            `json:"generated_at"`
	Files        map[string]string `json:"files"`
	Config       SiteConfig        `json:"config"`
	HistoryLimit int               `json:"history_limit"`
	HistoryTotal int               `json:"history_total"`
}

func writeJSON(dir, label string, v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	name := label + "." + hex.EncodeToString(h[:8]) + ".json"
	if e = os.WriteFile(filepath.Join(dir, name), b, 0644); e != nil {
		return "", e
	}
	return name, nil
}

// ExportSite publishes explicit fields only; SQLite/checkpoint state is never copied to Pages.
func (s *Store) ExportSite(dir string, config SiteConfig) error {
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	id, e := s.DatasetID()
	if e != nil {
		return e
	}
	manifest := PublishedManifest{Schema: 1, DatasetID: id, GeneratedAt: now(), Files: map[string]string{}, Config: config, HistoryLimit: 5000}
	programs, e := s.Maps(`SELECT r.id,r.entity,r.platform,r.program_id,r.label,r.url,r.bounty,r.data,
 (SELECT count(*) FROM records a WHERE a.program_id=r.id AND a.entity='asset' AND a.active=1) AS asset_count,
 (SELECT count(*) FROM records a WHERE a.program_id=r.id AND a.entity='asset' AND a.active=1 AND a.scope='in_scope') AS in_scope_count
 FROM records r WHERE r.entity='program' AND r.active=1 ORDER BY r.label COLLATE NOCASE,r.id`)
	if e != nil {
		return e
	}
	details, e := s.Maps("SELECT id,entity,platform,program_id,label,type,scope,conflict,data,last_seen FROM records WHERE entity='asset' AND active=1 ORDER BY label,id")
	if e != nil {
		return e
	}
	byProgram := map[string][]map[string]any{}
	indexes := map[string][]map[string]any{"hackerone": {}, "bugcrowd": {}}
	for _, r := range details {
		p := r["program_id"].(string)
		byProgram[p] = append(byProgram[p], r)
		summary := map[string]any{}
		for _, k := range []string{"id", "entity", "platform", "program_id", "label", "type", "scope", "conflict"} {
			summary[k] = r[k]
		}
		platform := r["platform"].(string)
		indexes[platform] = append(indexes[platform], summary)
	}
	for _, p := range programs {
		pid := p["id"].(string)
		rows := byProgram[pid]
		if rows == nil {
			rows = []map[string]any{}
		}
		name, err := writeJSON(dir, "detail-"+hash(pid)[:12], rows)
		if err != nil {
			return err
		}
		p["detail_file"] = name
	}
	if manifest.Files["programs"], e = writeJSON(dir, "programs", programs); e != nil {
		return e
	}
	for _, p := range Platforms {
		if manifest.Files["assets-"+p], e = writeJSON(dir, "assets-"+p, indexes[p]); e != nil {
			return e
		}
	}
	changes, e := s.Maps(`SELECT c.id,c.platform,c.program_id,c.entity,c.kind,c.label,c.before_data,c.after_data,c.created_at,p.label AS program_name,p.url AS program_url FROM changes c JOIN records p ON p.id=c.program_id ORDER BY c.id DESC LIMIT 5000`)
	if e != nil {
		return e
	}
	if manifest.Files["changes"], e = writeJSON(dir, "changes", changes); e != nil {
		return e
	}
	if e = s.DB.QueryRow("SELECT count(*) FROM changes").Scan(&manifest.HistoryTotal); e != nil {
		return e
	}
	overview, e := s.Overview()
	if e != nil {
		return e
	}
	delete(overview, "settings")
	counts := overview["counts"].(map[string]int)
	delete(counts, "watched")
	delete(counts, "unread")
	if manifest.Files["overview"], e = writeJSON(dir, "overview", overview); e != nil {
		return e
	}
	runs, e := s.Maps("SELECT * FROM runs ORDER BY id DESC LIMIT 50")
	if e != nil {
		return e
	}
	if manifest.Files["runs"], e = writeJSON(dir, "runs", runs); e != nil {
		return e
	}
	manifest.Version = hash(manifest)
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return e
	}
	temp := filepath.Join(dir, "manifest.json.tmp")
	if e = os.WriteFile(temp, raw, 0644); e != nil {
		return e
	}
	return os.Rename(temp, filepath.Join(dir, "manifest.json"))
}
