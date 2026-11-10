package monitor

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type checkpointFile struct {
	Name   string `json:"name"`
	Table  string `json:"table"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
}
type checkpointManifest struct {
	Schema int              `json:"schema"`
	Files  []checkpointFile `json:"files"`
}

var checkpointColumns = map[string][]string{
	"sources":       {"platform", "enabled", "status", "last_attempt", "last_success", "next_run", "error", "etag", "content_hash", "program_count", "asset_count"},
	"records":       {"id", "entity", "platform", "program_id", "label", "url", "type", "scope", "bounty", "conflict", "data", "active", "first_seen", "last_seen"},
	"changes":       {"id", "platform", "program_id", "entity", "kind", "label", "before_data", "after_data", "created_at"},
	"runs":          {"id", "platform", "status", "started_at", "finished_at", "programs", "assets", "changes", "message"},
	"site_metadata": {"key", "value"},
}

func (s *Store) DatasetID() (string, error) {
	if _, err := s.DB.Exec("CREATE TABLE IF NOT EXISTS site_metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)"); err != nil {
		return "", err
	}
	var id string
	err := s.DB.QueryRow("SELECT value FROM site_metadata WHERE key='dataset_id'").Scan(&id)
	if err == sql.ErrNoRows {
		var b [16]byte
		if _, err = rand.Read(b[:]); err != nil {
			return "", err
		}
		id = hex.EncodeToString(b[:])
		_, err = s.DB.Exec("INSERT INTO site_metadata VALUES('dataset_id',?)", id)
	}
	return id, err
}
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SaveCheckpoint stores only monitoring state, never browser preferences or credentials.
// Stable JSONL shards permit Git delta compression and avoid artifact/cache expiry.
func (s *Store) SaveCheckpoint(dir string) error {
	if _, err := s.DatasetID(); err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".checkpoint-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	manifest := checkpointManifest{Schema: 1, Files: []checkpointFile{}}
	for _, table := range []string{"sources", "records", "changes", "runs", "site_metadata"} {
		columns := checkpointColumns[table]
		order := "id"
		if table == "sources" {
			order = "platform"
		}
		if table == "site_metadata" {
			order = "key"
		}
		query := "SELECT " + strings.Join(columns, ",") + " FROM " + table + " ORDER BY " + order
		if table == "runs" {
			query = "SELECT " + strings.Join(columns, ",") + " FROM (SELECT * FROM runs ORDER BY id DESC LIMIT 100) ORDER BY id"
		}
		// Keep records ordered by ID and changes in stable chronological shards.
		rows, e := s.Maps(query)
		if e != nil {
			return e
		}
		for start, part := 0, 0; start < len(rows) || part == 0; start, part = start+5000, part+1 {
			end := min(start+5000, len(rows))
			name := fmt.Sprintf("%s-%06d.jsonl", table, part)
			path := filepath.Join(stage, name)
			f, e := os.Create(path)
			if e != nil {
				return e
			}
			writer := bufio.NewWriter(f)
			encoder := json.NewEncoder(writer)
			for _, row := range rows[start:end] {
				if e = encoder.Encode(row); e != nil {
					f.Close()
					return e
				}
			}
			if e = writer.Flush(); e != nil {
				f.Close()
				return e
			}
			if e = f.Close(); e != nil {
				return e
			}
			checksum, e := fileHash(path)
			if e != nil {
				return e
			}
			manifest.Files = append(manifest.Files, checkpointFile{Name: name, Table: table, SHA256: checksum, Rows: end - start})
			if len(rows) == 0 {
				break
			}
		}
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "manifest.json"), b, 0644); err != nil {
		return err
	}
	// The target is a generated directory; the checked-out state branch retains its history.
	backup := dir + ".previous"
	if _, err = os.Stat(backup); err == nil {
		return fmt.Errorf("checkpoint backup already exists: %s", backup)
	}
	exists := false
	if _, err = os.Stat(dir); err == nil {
		exists = true
		if err = os.Rename(dir, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(stage, dir); err != nil {
		if exists {
			_ = os.Rename(backup, dir)
		}
		return err
	}
	if exists {
		return os.RemoveAll(backup)
	}
	return nil
}

func (s *Store) RestoreCheckpoint(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest checkpointManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Schema != 1 {
		return errors.New("unsupported checkpoint schema; refusing to reset the baseline")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, file := range manifest.Files {
		if _, ok := checkpointColumns[file.Table]; !ok || filepath.Base(file.Name) != file.Name || names[file.Name] || file.Rows < 0 {
			return errors.New("invalid checkpoint manifest")
		}
		names[file.Name] = true
		seen[file.Table] = true
		sum, e := fileHash(filepath.Join(dir, file.Name))
		if e != nil {
			return e
		}
		if sum != file.SHA256 {
			return fmt.Errorf("checkpoint checksum mismatch: %s", file.Name)
		}
	}
	for table := range checkpointColumns {
		if !seen[table] {
			return fmt.Errorf("checkpoint is missing table %s", table)
		}
	}
	if _, err = s.DatasetID(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("restore requires an empty working database")
	}
	for table := range checkpointColumns {
		if _, err = tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	for _, file := range manifest.Files {
		columns := checkpointColumns[file.Table]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(columns)), ",")
		stmt, e := tx.Prepare("INSERT INTO " + file.Table + "(" + strings.Join(columns, ",") + ") VALUES(" + placeholders + ")")
		if e != nil {
			return e
		}
		f, e := os.Open(filepath.Join(dir, file.Name))
		if e != nil {
			stmt.Close()
			return e
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
		lines := 0
		for scanner.Scan() {
			var row map[string]json.RawMessage
			if e = json.Unmarshal(scanner.Bytes(), &row); e != nil {
				break
			}
			args := make([]any, len(columns))
			for i, k := range columns {
				v, ok := row[k]
				if !ok {
					e = fmt.Errorf("missing checkpoint column %s", k)
					break
				}
				if k == "data" || k == "before_data" || k == "after_data" {
					if string(v) != "null" {
						var record Record
						if e = json.Unmarshal(v, &record); e != nil {
							break
						}
						args[i] = encode(record)
					}
				} else {
					e = json.Unmarshal(v, &args[i])
					if e != nil {
						break
					}
				}
			}
			if e != nil {
				break
			}
			if _, e = stmt.Exec(args...); e != nil {
				break
			}
			lines++
		}
		if e == nil {
			e = scanner.Err()
		}
		f.Close()
		stmt.Close()
		if e != nil {
			return fmt.Errorf("restore %s: %w", file.Name, e)
		}
		if lines != file.Rows {
			return fmt.Errorf("checkpoint row count mismatch: %s", file.Name)
		}
	}
	if err = tx.QueryRow("SELECT count(*) FROM sources WHERE platform IN ('hackerone','bugcrowd')").Scan(&count); err != nil || count != 2 {
		return errors.New("checkpoint sources missing")
	}
	var id string
	if err = tx.QueryRow("SELECT value FROM site_metadata WHERE key='dataset_id'").Scan(&id); err != nil || id == "" {
		return errors.New("checkpoint dataset identity missing")
	}
	return tx.Commit()
}
