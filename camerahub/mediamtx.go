package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MediaMTX is a minimal client for the MediaMTX control API (v3).
type MediaMTX struct {
	base string
	http *http.Client
}

func NewMediaMTX(base string) *MediaMTX {
	return &MediaMTX{base: base, http: &http.Client{Timeout: 10 * time.Second}}
}

// PathStatus is the runtime state of a path, from /v3/paths/list.
type PathStatus struct {
	Name          string     `json:"name"`
	Ready         bool       `json:"ready"`
	ReadyTime     *time.Time `json:"readyTime"`
	Tracks        []string   `json:"tracks"`
	BytesReceived int64      `json:"bytesReceived"`
	Readers       []any      `json:"readers"`
}

func (m *MediaMTX) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.base+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("mediamtx %s %s: %d %s", method, path, resp.StatusCode, bytes.TrimSpace(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// UpsertPath replaces the path config, or adds it when it does not exist yet.
func (m *MediaMTX) UpsertPath(ctx context.Context, name string, cfg map[string]any) error {
	p := url.PathEscape(name)
	if _, err := m.do(ctx, http.MethodPost, "/v3/config/paths/replace/"+p, cfg, nil); err == nil {
		return nil
	}
	_, err := m.do(ctx, http.MethodPost, "/v3/config/paths/add/"+p, cfg, nil)
	return err
}

func (m *MediaMTX) DeletePath(ctx context.Context, name string) error {
	code, err := m.do(ctx, http.MethodDelete, "/v3/config/paths/delete/"+url.PathEscape(name), nil, nil)
	if code == http.StatusNotFound {
		return nil
	}
	return err
}

// ConfiguredPaths returns the names of paths currently in the MediaMTX config.
func (m *MediaMTX) ConfiguredPaths(ctx context.Context) (map[string]bool, error) {
	var res struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if _, err := m.do(ctx, http.MethodGet, "/v3/config/paths/list?itemsPerPage=10000", nil, &res); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(res.Items))
	for _, it := range res.Items {
		out[it.Name] = true
	}
	return out, nil
}

// Paths returns runtime status for every active path.
func (m *MediaMTX) Paths(ctx context.Context) (map[string]PathStatus, error) {
	var res struct {
		Items []PathStatus `json:"items"`
	}
	if _, err := m.do(ctx, http.MethodGet, "/v3/paths/list?itemsPerPage=10000", nil, &res); err != nil {
		return nil, err
	}
	out := make(map[string]PathStatus, len(res.Items))
	for _, it := range res.Items {
		out[it.Name] = it
	}
	return out, nil
}
