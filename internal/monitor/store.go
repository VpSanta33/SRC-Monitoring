package monitor

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Store struct{ DB *sql.DB }
type Source struct {
	Platform     string `json:"platform"`
	Enabled      bool   `json:"enabled"`
	Status       string `json:"status"`
	LastAttempt  string `json:"last_attempt"`
	LastSuccess  string `json:"last_success"`
	NextRun      string `json:"next_run"`
	Error        string `json:"error"`
	ETag         string `json:"-"`
	Hash         string `json:"-"`
	ProgramCount int    `json:"program_count"`
	AssetCount   int    `json:"asset_count"`
	URL          string `json:"url"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=10000;
 CREATE TABLE IF NOT EXISTS sources(platform TEXT PRIMARY KEY,enabled INTEGER NOT NULL DEFAULT 1,status TEXT NOT NULL DEFAULT 'pending',last_attempt TEXT NOT NULL DEFAULT '',last_success TEXT NOT NULL DEFAULT '',next_run TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',etag TEXT NOT NULL DEFAULT '',content_hash TEXT NOT NULL DEFAULT '',program_count INTEGER NOT NULL DEFAULT 0,asset_count INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value INTEGER NOT NULL);
 INSERT OR IGNORE INTO settings VALUES('interval_minutes',30);
 CREATE TABLE IF NOT EXISTS records(id TEXT PRIMARY KEY,entity TEXT NOT NULL,platform TEXT NOT NULL,program_id TEXT NOT NULL,label TEXT NOT NULL,url TEXT NOT NULL,type TEXT NOT NULL,scope TEXT NOT NULL,bounty INTEGER NOT NULL,conflict INTEGER NOT NULL,data TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 1,watched INTEGER NOT NULL DEFAULT 0,first_seen TEXT NOT NULL,last_seen TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS records_entity ON records(entity,active,platform);
 CREATE INDEX IF NOT EXISTS records_program ON records(program_id,entity,active);
 CREATE TABLE IF NOT EXISTS changes(id INTEGER PRIMARY KEY,platform TEXT NOT NULL,program_id TEXT NOT NULL,entity TEXT NOT NULL,kind TEXT NOT NULL,label TEXT NOT NULL,before_data TEXT,after_data TEXT,created_at TEXT NOT NULL,seen INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS changes_time ON changes(created_at);
 CREATE TABLE IF NOT EXISTS runs(id INTEGER PRIMARY KEY,platform TEXT NOT NULL,status TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT NOT NULL DEFAULT '',programs INTEGER NOT NULL DEFAULT 0,assets INTEGER NOT NULL DEFAULT 0,changes INTEGER NOT NULL DEFAULT 0,message TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS snapshots(id INTEGER PRIMARY KEY,platform TEXT NOT NULL,created_at TEXT NOT NULL,content_hash TEXT NOT NULL,payload BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, p := range Platforms {
		if _, err = db.Exec("INSERT OR IGNORE INTO sources(platform) VALUES(?)", p); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err = db.Exec("UPDATE runs SET status='failed',finished_at=?,message='服务重启中断了同步' WHERE status='running'", now()); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("UPDATE sources SET status='error',error='服务重启中断了同步' WHERE status='running'"); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Sources() ([]Source, error) {
	rows, err := s.DB.Query("SELECT platform,enabled,status,last_attempt,last_success,next_run,error,etag,content_hash,program_count,asset_count FROM sources ORDER BY platform DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var v Source
		if err := rows.Scan(&v.Platform, &v.Enabled, &v.Status, &v.LastAttempt, &v.LastSuccess, &v.NextRun, &v.Error, &v.ETag, &v.Hash, &v.ProgramCount, &v.AssetCount); err != nil {
			return nil, err
		}
		v.URL = SourceURLs[v.Platform]
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Interval() (int, error) {
	var n int
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key='interval_minutes'").Scan(&n)
	return n, err
}
func (s *Store) BeginRun(p string) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sources SET status='running',last_attempt=?,error='' WHERE platform=?", now(), p); err != nil {
		return 0, err
	}
	r, err := tx.Exec("INSERT INTO runs(platform,status,started_at) VALUES(?,'running',?)", p, now())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Store) FailRun(p string, id int64, cause error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sources SET status='error',error=? WHERE platform=?", cause.Error(), p); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE runs SET status='failed',finished_at=?,message=? WHERE id=?", now(), cause.Error(), id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ApplySnapshot(p string, raw []byte, id int64, etag string) (int, error) {
	snapshot, err := Normalize(p, raw)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var lastSuccess, lastHash string
	var oldPrograms, oldAssets int
	if err = tx.QueryRow("SELECT last_success,content_hash,program_count,asset_count FROM sources WHERE platform=?", p).Scan(&lastSuccess, &lastHash, &oldPrograms, &oldAssets); err != nil {
		return 0, err
	}
	baseline := lastSuccess == ""
	if !baseline && ((oldPrograms > 0 && float64(snapshot.Programs) < float64(oldPrograms)*0.7) || (oldAssets > 0 && float64(snapshot.Assets) < float64(oldAssets)*0.7)) {
		return 0, errors.New("项目或资产数量下降超过 30%，拒绝异常快照，已有数据保持不变")
	}
	type previous struct {
		Data   string
		Active bool
	}
	old := map[string]previous{}
	rows, err := tx.Query("SELECT id,data,active FROM records WHERE platform=?", p)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var key string
		var v previous
		if err = rows.Scan(&key, &v.Data, &v.Active); err != nil {
			rows.Close()
			return 0, err
		}
		old[key] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	stamp, changes := now(), 0
	upsert, err := tx.Prepare(`INSERT INTO records(id,entity,platform,program_id,label,url,type,scope,bounty,conflict,data,active,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET label=excluded.label,url=excluded.url,type=excluded.type,scope=excluded.scope,bounty=excluded.bounty,conflict=excluded.conflict,data=excluded.data,active=1,last_seen=excluded.last_seen`)
	if err != nil {
		return 0, err
	}
	defer upsert.Close()
	addChange := func(r Record, kind string, before, after any) error {
		_, e := tx.Exec("INSERT INTO changes(platform,program_id,entity,kind,label,before_data,after_data,created_at) VALUES(?,?,?,?,?,?,?,?)", p, r.ProgramID, r.Entity, kind, r.Label, before, after, stamp)
		if e == nil {
			changes++
		}
		return e
	}
	for key, r := range snapshot.Records {
		data := encode(r)
		prev, exists := old[key]
		if !baseline && (!exists || !prev.Active || prev.Data != data) {
			kind := "modified"
			var before any = prev.Data
			if !exists || !prev.Active {
				kind = "added"
				before = nil
			}
			if err = addChange(r, kind, before, data); err != nil {
				return 0, err
			}
		}
		if _, err = upsert.Exec(r.ID, r.Entity, r.Platform, r.ProgramID, r.Label, r.URL, r.Type, r.Scope, r.Bounty, r.Conflict, data, stamp, stamp); err != nil {
			return 0, err
		}
	}
	for key, prev := range old {
		if _, exists := snapshot.Records[key]; !exists && prev.Active {
			var r Record
			if err = json.Unmarshal([]byte(prev.Data), &r); err != nil {
				return 0, err
			}
			if err = addChange(r, "removed", prev.Data, nil); err != nil {
				return 0, err
			}
			if _, err = tx.Exec("UPDATE records SET active=0 WHERE id=?", key); err != nil {
				return 0, err
			}
		}
	}
	contentHash := hash(json.RawMessage(raw))
	if contentHash != lastHash {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err = z.Write(raw); err != nil {
			return 0, err
		}
		if err = z.Close(); err != nil {
			return 0, err
		}
		if _, err = tx.Exec("INSERT INTO snapshots(platform,created_at,content_hash,payload) VALUES(?,?,?,?)", p, stamp, contentHash, buf.Bytes()); err != nil {
			return 0, err
		}
		if _, err = tx.Exec("DELETE FROM snapshots WHERE platform=? AND id NOT IN (SELECT id FROM snapshots WHERE platform=? ORDER BY id DESC LIMIT 20)", p, p); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec("UPDATE sources SET status='healthy',last_success=?,error='',etag=?,content_hash=?,program_count=?,asset_count=? WHERE platform=?", stamp, etag, contentHash, snapshot.Programs, snapshot.Assets, p); err != nil {
		return 0, err
	}
	message := "快照比对完成"
	if baseline {
		message = "首次同步，已建立基线"
	}
	if _, err = tx.Exec("UPDATE runs SET status='success',finished_at=?,programs=?,assets=?,changes=?,message=? WHERE id=?", stamp, snapshot.Programs, snapshot.Assets, changes, message, id); err != nil {
		return 0, err
	}
	return changes, tx.Commit()
}
func (s *Store) Unchanged(p string, id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sources SET status='healthy',last_success=?,error='' WHERE platform=?", now(), p); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE runs SET status='success',finished_at=?,message='远端快照未变化（304）',programs=(SELECT program_count FROM sources WHERE platform=?),assets=(SELECT asset_count FROM sources WHERE platform=?) WHERE id=?", now(), p, p, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Maps closes rows before returning so callers can safely reuse the single writer connection.
func (s *Store) Maps(query string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, k := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if (k == "data" || k == "before_data" || k == "after_data") && v != nil {
				var decoded any
				if err = json.Unmarshal([]byte(v.(string)), &decoded); err != nil {
					return nil, err
				}
				v = decoded
			}
			row[k] = v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type ListResult struct {
	Items []map[string]any `json:"items"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Limit int              `json:"limit"`
}

func (s *Store) List(entity string, f url.Values, export bool) (ListResult, error) {
	result := ListResult{Page: 1, Limit: 25}
	if f.Get("page") != "" {
		p, e := strconv.Atoi(f.Get("page"))
		if e != nil || p < 1 || p > 1000000 {
			return result, errors.New("页码无效")
		}
		result.Page = p
	}
	if f.Get("limit") != "" {
		n, e := strconv.Atoi(f.Get("limit"))
		if e != nil || n < 1 || n > 100 {
			return result, errors.New("每页数量应为 1–100")
		}
		result.Limit = n
	}
	alias, selectSQL, from, order := "r", "r.*,p.label AS program_name,p.url AS program_url,p.watched AS program_watched", "records r JOIN records p ON p.id=r.program_id", "r.label COLLATE NOCASE"
	clauses := []string{}
	args := []any{}
	switch entity {
	case "programs":
		clauses = append(clauses, "r.entity='program'", "r.active=1")
		selectSQL += ",(SELECT count(*) FROM records a WHERE a.program_id=r.id AND a.entity='asset' AND a.active=1) AS asset_count,(SELECT count(*) FROM records a WHERE a.program_id=r.id AND a.entity='asset' AND a.active=1 AND a.scope='in_scope') AS in_scope_count"
		order = "r.watched DESC,r.label COLLATE NOCASE"
	case "assets":
		clauses = append(clauses, "r.entity='asset'", "r.active=1")
	case "changes":
		alias = "c"
		selectSQL = "c.*,p.label AS program_name,p.url AS program_url,p.watched AS program_watched"
		from = "changes c JOIN records p ON p.id=c.program_id"
		order = "c.id DESC"
	default:
		return result, errors.New("未知列表类型")
	}
	if v := f.Get("platform"); v != "" {
		clauses = append(clauses, alias+".platform=?")
		args = append(args, v)
	}
	if v := f.Get("q"); v != "" {
		v = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v)
		clauses = append(clauses, "("+alias+".label LIKE ? ESCAPE '\\' OR p.label LIKE ? ESCAPE '\\')")
		args = append(args, "%"+v+"%", "%"+v+"%")
	}
	if f.Get("watched") == "1" {
		clauses = append(clauses, "p.watched=1")
	}
	if entity == "programs" && f.Get("bounty") == "1" {
		clauses = append(clauses, "r.bounty=1")
	}
	fields := []string{}
	if entity == "assets" {
		fields = []string{"scope", "type", "program_id"}
	}
	if entity == "changes" {
		fields = []string{"kind", "program_id"}
		if f.Get("unread") == "1" {
			clauses = append(clauses, "c.seen=0")
		}
	}
	for _, k := range fields {
		if v := f.Get(k); v != "" {
			clauses = append(clauses, alias+"."+k+"=?")
			args = append(args, v)
		}
	}
	base := " FROM " + from
	if len(clauses) > 0 {
		base += " WHERE " + strings.Join(clauses, " AND ")
	}
	if err := s.DB.QueryRow("SELECT count(*)"+base, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	query := "SELECT " + selectSQL + base + " ORDER BY " + order
	if !export {
		query += " LIMIT ? OFFSET ?"
		args = append(args, result.Limit, (result.Page-1)*result.Limit)
	}
	var err error
	result.Items, err = s.Maps(query, args...)
	return result, err
}
func (s *Store) Overview() (map[string]any, error) {
	counts := map[string]int{}
	queries := map[string]string{"programs": "SELECT count(*) FROM records WHERE entity='program' AND active=1", "assets": "SELECT count(*) FROM records WHERE entity='asset' AND active=1", "in_scope": "SELECT count(*) FROM records WHERE entity='asset' AND active=1 AND scope='in_scope'", "watched": "SELECT count(*) FROM records WHERE entity='program' AND active=1 AND watched=1", "changes_24h": "SELECT count(*) FROM changes WHERE julianday(created_at)>=julianday('now','-1 day')", "unread": "SELECT count(*) FROM changes WHERE seen=0"}
	for k, q := range queries {
		var n int
		if err := s.DB.QueryRow(q).Scan(&n); err != nil {
			return nil, err
		}
		counts[k] = n
	}
	sources, err := s.Sources()
	if err != nil {
		return nil, err
	}
	interval, err := s.Interval()
	if err != nil {
		return nil, err
	}
	types, err := s.Maps("SELECT type,count(*) AS count FROM records WHERE entity='asset' AND active=1 GROUP BY type ORDER BY count DESC LIMIT 6")
	if err != nil {
		return nil, err
	}
	trend, err := s.Maps("SELECT substr(created_at,1,10) AS day,kind,count(*) AS count FROM changes WHERE julianday(created_at)>=julianday('now','-7 days') GROUP BY day,kind ORDER BY day")
	if err != nil {
		return nil, err
	}
	return map[string]any{"counts": counts, "sources": sources, "types": types, "trend": trend, "settings": map[string]int{"interval_minutes": interval}}, nil
}
func (s *Store) SaveSettings(minutes int, enabled map[string]bool) error {
	if minutes < 5 || minutes > 1440 || len(enabled) != 2 {
		return errors.New("同步间隔需为 5–1440 分钟，并提供两个平台的启用状态")
	}
	for _, p := range Platforms {
		if _, ok := enabled[p]; !ok {
			return fmt.Errorf("缺少平台 %s", p)
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE settings SET value=? WHERE key='interval_minutes'", minutes); err != nil {
		return err
	}
	due := time.Now().Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339)
	for p, v := range enabled {
		if _, err = tx.Exec("UPDATE sources SET enabled=?,next_run=? WHERE platform=?", v, due, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}
