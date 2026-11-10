package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type Syncer struct {
	Store  *Store
	Client *http.Client
	URLs   map[string]string
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSyncer(s *Store) *Syncer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Syncer{Store: s, Client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || req.URL.Scheme != "https" || req.URL.Host != "raw.githubusercontent.com" {
			return errors.New("拒绝非预期的数据源重定向")
		}
		return nil
	}}, URLs: SourceURLs, ctx: ctx, cancel: cancel}
}
func (m *Syncer) Fetch(p, etag string) ([]byte, string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(m.ctx, http.MethodGet, m.URLs[p], nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", "SRC-Observatory/1.0")
		req.Header.Set("Accept", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := m.Client.Do(req)
		delay := time.Duration(1<<uint(attempt+1)) * time.Second
		if err == nil {
			if resp.StatusCode == http.StatusNotModified {
				resp.Body.Close()
				return nil, etag, nil
			}
			if resp.StatusCode == http.StatusOK {
				const maxBytes = 60 << 20
				body, e := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
				resp.Body.Close()
				if e != nil {
					return nil, "", fmt.Errorf("读取数据源失败: %w", e)
				}
				if len(body) > maxBytes {
					return nil, "", errors.New("快照超过 60 MB 上限")
				}
				if len(body) == 0 {
					return nil, "", errors.New("远端返回空响应")
				}
				return body, resp.Header.Get("ETag"), nil
			}
			resp.Body.Close()
			err = fmt.Errorf("数据源返回 HTTP %d，保留上次快照", resp.StatusCode)
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return nil, "", err
			}
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n >= 0 {
				delay = time.Duration(min(n, 60)) * time.Second
			}
		}
		if attempt == 2 {
			return nil, "", fmt.Errorf("同步失败: %w", err)
		}
		select {
		case <-m.ctx.Done():
			return nil, "", m.ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, "", errors.New("同步失败")
}
func (m *Syncer) run(source Source) error {
	id, err := m.Store.BeginRun(source.Platform)
	if err != nil {
		log.Printf("begin sync: %v", err)
		return err
	}
	body, etag, err := m.Fetch(source.Platform, source.ETag)
	changes := 0
	if err == nil {
		if body == nil {
			if source.LastSuccess == "" {
				err = errors.New("无基线时收到 304 响应")
			} else {
				err = m.Store.Unchanged(source.Platform, id)
			}
		} else {
			changes, err = m.Store.ApplySnapshot(source.Platform, body, id, etag)
		}
	}
	if err != nil {
		log.Printf("sync %s: %v", source.Platform, err)
		if e := m.Store.FailRun(source.Platform, id, err); e != nil {
			log.Printf("record failure: %v", e)
		}
	} else {
		log.Printf("sync %s complete, %d changes", source.Platform, changes)
	}
	return err
}
func (m *Syncer) Close() { m.cancel() }

// Collect runs each enabled source once. The workflow owns the recurring schedule.
func (m *Syncer) Collect() error {
	sources, err := m.Store.Sources()
	if err != nil {
		return err
	}
	var failures []error
	for _, source := range sources {
		if source.Enabled {
			if err = m.run(source); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
