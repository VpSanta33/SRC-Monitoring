package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

var SourceURLs = map[string]string{
	"hackerone": "https://raw.githubusercontent.com/arkadiyt/bounty-targets-data/main/data/hackerone_data.json",
	"bugcrowd":  "https://raw.githubusercontent.com/arkadiyt/bounty-targets-data/main/data/bugcrowd_data.json",
}
var Platforms = []string{"hackerone", "bugcrowd"}

type Record struct {
	ID        string           `json:"id"`
	Entity    string           `json:"entity"`
	Platform  string           `json:"platform"`
	ProgramID string           `json:"program_id"`
	Label     string           `json:"label"`
	URL       string           `json:"url,omitempty"`
	Type      string           `json:"type,omitempty"`
	Scope     string           `json:"scope,omitempty"`
	Bounty    bool             `json:"bounty"`
	Conflict  bool             `json:"conflict"`
	Scopes    []string         `json:"source_scopes,omitempty"`
	Metadata  []map[string]any `json:"metadata"`
}

type Snapshot struct {
	Records  map[string]Record
	Programs int
	Assets   int
}

func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func hash(v any) string   { h := sha256.Sum256([]byte(encode(v))); return hex.EncodeToString(h[:]) }
func text(v any) string   { s, _ := v.(string); return s }

func Normalize(platform string, raw []byte) (Snapshot, error) {
	result := Snapshot{Records: map[string]Record{}}
	if _, ok := SourceURLs[platform]; !ok {
		return result, errors.New("未知平台")
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return result, errors.New("空数据或无效 JSON，保留已有快照")
	}
	for _, row := range rows {
		name, link := strings.TrimSpace(text(row["name"])), text(row["url"])
		u, err := url.Parse(link)
		if err != nil || u.Scheme != "https" || name == "" || (platform == "hackerone" && u.Host != "hackerone.com") || (platform == "bugcrowd" && u.Host != "bugcrowd.com" && u.Host != "www.bugcrowd.com") {
			return result, errors.New("项目缺少名称或可信平台链接")
		}
		handle := text(row["handle"])
		if platform == "bugcrowd" {
			handle = strings.Trim(u.Path, "/")
		}
		if handle == "" {
			return result, errors.New("项目缺少稳定标识")
		}
		id := platform + ":" + handle
		if _, exists := result.Records[id]; exists {
			return result, errors.New("项目标识重复")
		}
		meta := map[string]any{}
		for _, k := range []string{"offers_bounties", "submission_state", "max_payout", "safe_harbor"} {
			if v, ok := row[k]; ok {
				meta[k] = v
			}
		}
		bounty, _ := row["offers_bounties"].(bool)
		if platform == "bugcrowd" {
			payout, _ := row["max_payout"].(float64)
			bounty = payout > 0
		}
		result.Records[id] = Record{ID: id, Entity: "program", Platform: platform, ProgramID: id, Label: name, URL: link, Bounty: bounty, Metadata: []map[string]any{meta}}
		result.Programs++
		targets, ok := row["targets"].(map[string]any)
		if !ok {
			return result, errors.New("项目缺少范围数据")
		}
		for _, scope := range []string{"in_scope", "out_of_scope"} {
			items, ok := targets[scope].([]any)
			if !ok {
				return result, fmt.Errorf("项目 %s 缺少完整 %s 列表", name, scope)
			}
			for _, rawTarget := range items {
				target, ok := rawTarget.(map[string]any)
				if !ok {
					return result, errors.New("资产结构无效")
				}
				value, kind := text(target["asset_identifier"]), text(target["asset_type"])
				if platform == "bugcrowd" {
					value, kind = text(target["target"]), text(target["type"])
				}
				value = strings.TrimSpace(value)
				if value == "" || kind == "" {
					return result, errors.New("资产缺少标识或类型")
				}
				aid := hash([]string{id, kind, value})
				asset, exists := result.Records[aid]
				if !exists {
					asset = Record{ID: aid, Entity: "asset", Platform: platform, ProgramID: id, Label: value, Type: kind, Scope: scope, Metadata: []map[string]any{}}
					result.Assets++
				}
				if scope == "out_of_scope" {
					asset.Scope = scope
				}
				if !contains(asset.Scopes, scope) {
					asset.Scopes = append(asset.Scopes, scope)
				}
				target["source_scope"] = scope
				duplicate := false
				for _, m := range asset.Metadata {
					if encode(m) == encode(target) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					asset.Metadata = append(asset.Metadata, target)
				}
				asset.Conflict = len(asset.Scopes) > 1
				result.Records[aid] = asset
			}
		}
	}
	for k, r := range result.Records {
		sort.Strings(r.Scopes)
		sort.Slice(r.Metadata, func(i, j int) bool { return encode(r.Metadata[i]) < encode(r.Metadata[j]) })
		result.Records[k] = r
	}
	return result, nil
}
func contains(values []string, v string) bool {
	for _, a := range values {
		if a == v {
			return true
		}
	}
	return false
}
