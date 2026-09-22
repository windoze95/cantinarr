// Package tdarr reads processing activity and library statistics. It never
// forwards arbitrary database operations or exposes Tdarr configuration.
package tdarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

var ErrLibraryNotFound = errors.New("Tdarr library no longer exists")

type Worker struct {
	ID       string   `json:"id"`
	File     string   `json:"file"`
	Kind     string   `json:"kind"`
	Compute  string   `json:"compute"`
	Status   string   `json:"status"`
	Progress *float64 `json:"progress_percent"`
	FPS      *float64 `json:"fps"`
	ETA      string   `json:"eta"`
	Flow     bool     `json:"flow"`
}

type Node struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Paused  bool     `json:"paused"`
	Workers []Worker `json:"workers"`
}

type Activity struct {
	ObservedAt time.Time `json:"observed_at"`
	Nodes      []Node    `json:"nodes"`
}

type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Libraries struct {
	ObservedAt time.Time `json:"observed_at"`
	Items      []Library `json:"items"`
}

type Count struct {
	Label string `json:"label"`
	Value *int64 `json:"value"`
}

type Stats struct {
	ObservedAt   time.Time `json:"observed_at"`
	LibraryID    string    `json:"library_id"`
	TotalFiles   int64     `json:"total_files"`
	Note         string    `json:"note"`
	Transcodes   []Count   `json:"transcodes"`
	HealthChecks []Count   `json:"health_checks"`
}

type cacheEntry struct {
	value   any
	expires time.Time
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	mu      sync.Mutex
	cache   map[string]cacheEntry
	flight  singleflight.Group
	now     func() time.Time
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey,
		http: &http.Client{Transport: httpx.Internal(), Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		cache: make(map[string]cacheEntry), now: time.Now,
	}
}

// request permits only the read operations assembled by this package. Error
// bodies and transport strings may contain keys/addresses and are never echoed.
func (c *Client) request(ctx context.Context, path string, body any, out any) error {
	method := http.MethodGet
	var data []byte
	if body != nil {
		method = http.MethodPost
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return errors.New("could not encode Tdarr read")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v2/"+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Tdarr server URL")
	}
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("Tdarr request cancelled or timed out")
		}
		return errors.New("could not reach Tdarr; check the server URL and connection")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return errors.New("Tdarr rejected the API key; check Tools > API Keys")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return errors.New("Tdarr redirected the request; use the final server API URL")
	case resp.StatusCode == 404:
		return errors.New("Tdarr API is unavailable; check the server API port and Tdarr version")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("Tdarr returned HTTP %d; retry shortly", resp.StatusCode)
	}
	const maxResponse = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse || json.Unmarshal(raw, out) != nil {
		return errors.New("Tdarr returned an incompatible response")
	}
	return nil
}

func dbRead(collection, mode, id string) any {
	return map[string]any{"data": map[string]any{"collection": collection, "mode": mode, "docID": id, "obj": map[string]any{}}}
}

func (c *Client) cached(ctx context.Context, key string, ttl time.Duration, read func(context.Context) (any, error)) (any, error) {
	lookup := func() (any, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		e, ok := c.cache[key]
		return e.value, ok && c.now().Before(e.expires)
	}
	if v, ok := lookup(); ok {
		return v, nil
	}
	result := c.flight.DoChan(key, func() (any, error) {
		if v, ok := lookup(); ok {
			return v, nil
		}
		// One caller leaving a screen must not cancel another caller's read.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		v, err := read(readCtx)
		if err == nil {
			c.mu.Lock()
			c.cache[key] = cacheEntry{v, c.now().Add(ttl)}
			c.mu.Unlock()
		}
		return v, err
	})
	select {
	case <-ctx.Done():
		return nil, errors.New("Tdarr request cancelled or timed out")
	case r := <-result:
		return r.Val, r.Err
	}
}

type rawWorker struct {
	File     string   `json:"file"`
	Type     string   `json:"workerType"`
	Idle     bool     `json:"idle"`
	Flow     bool     `json:"isFlowWorker"`
	Status   string   `json:"status"`
	Progress *float64 `json:"percentage"`
	FPS      *float64 `json:"fps"`
	ETA      string   `json:"ETA"`
}

func measurement(v *float64, max float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > max {
		return nil
	}
	return v
}

func (c *Client) Activity(ctx context.Context) (*Activity, error) {
	v, err := c.cached(ctx, "activity", 5*time.Second, func(ctx context.Context) (any, error) {
		var raw map[string]struct {
			Name    string               `json:"nodeName"`
			Paused  bool                 `json:"nodePaused"`
			Workers map[string]rawWorker `json:"workers"`
		}
		if err := c.request(ctx, "get-nodes", nil, &raw); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, errors.New("Tdarr returned incompatible node data")
		}
		out := &Activity{ObservedAt: c.now().UTC(), Nodes: []Node{}}
		for id, node := range raw {
			if node.Name == "" || node.Workers == nil {
				return nil, errors.New("Tdarr returned incompatible node data")
			}
			n := Node{ID: id, Name: node.Name, Paused: node.Paused, Workers: []Worker{}}
			for workerID, worker := range node.Workers {
				if worker.Idle {
					continue
				}
				kind, compute := "Other", ""
				switch worker.Type {
				case "transcodecpu":
					kind, compute = "Transcode", "CPU"
				case "transcodegpu":
					kind, compute = "Transcode", "GPU"
				case "healthcheckcpu":
					kind, compute = "Health check", "CPU"
				case "healthcheckgpu":
					kind, compute = "Health check", "GPU"
				}
				n.Workers = append(n.Workers, Worker{ID: workerID, File: worker.File, Kind: kind, Compute: compute,
					Status: worker.Status, Progress: measurement(worker.Progress, 100), FPS: measurement(worker.FPS, math.MaxFloat64), ETA: worker.ETA, Flow: worker.Flow})
			}
			sort.Slice(n.Workers, func(i, j int) bool { return n.Workers[i].ID < n.Workers[j].ID })
			out.Nodes = append(out.Nodes, n)
		}
		sort.Slice(out.Nodes, func(i, j int) bool {
			if out.Nodes[i].Name == out.Nodes[j].Name {
				return out.Nodes[i].ID < out.Nodes[j].ID
			}
			return out.Nodes[i].Name < out.Nodes[j].Name
		})
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Activity), nil
}

func (c *Client) Libraries(ctx context.Context) (*Libraries, error) {
	v, err := c.cached(ctx, "libraries", 30*time.Second, func(ctx context.Context) (any, error) {
		var raw []struct {
			ID   string `json:"_id"`
			Name string `json:"name"`
		}
		if err := c.request(ctx, "cruddb", dbRead("LibrarySettingsJSONDB", "getAll", ""), &raw); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, errors.New("Tdarr returned incompatible library data")
		}
		out := &Libraries{ObservedAt: c.now().UTC(), Items: []Library{}}
		for _, lib := range raw {
			if lib.ID == "" || lib.Name == "" {
				return nil, errors.New("Tdarr returned incompatible library data")
			}
			out.Items = append(out.Items, Library{ID: lib.ID, Name: lib.Name})
		}
		sort.Slice(out.Items, func(i, j int) bool {
			if out.Items[i].Name == out.Items[j].Name {
				return out.Items[i].ID < out.Items[j].ID
			}
			return out.Items[i].Name < out.Items[j].Name
		})
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Libraries), nil
}

func (c *Client) Stats(ctx context.Context, libraryID string) (*Stats, error) {
	// Validate before caching so arbitrary client input cannot grow the cache.
	if libraryID != "" {
		libs, err := c.Libraries(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, lib := range libs.Items {
			if lib.ID == libraryID {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrLibraryNotFound
		}
	}
	v, err := c.cached(ctx, "stats:"+libraryID, 30*time.Second, func(ctx context.Context) (any, error) {
		out := &Stats{LibraryID: libraryID, Transcodes: []Count{}, HealthChecks: []Count{}}
		if libraryID == "" {
			out.Note = "Tdarr's queue/category counts are not a breakdown of the file total. Files processing or awaiting acceptance may be absent."
			var raw struct {
				Total         *int64 `json:"totalFileCount"`
				Hold          *int64 `json:"table0Count"`
				Queued        *int64 `json:"table1Count"`
				Success       *int64 `json:"table2Count"`
				Error         *int64 `json:"table3Count"`
				HealthQueued  *int64 `json:"table4Count"`
				HealthSuccess *int64 `json:"table5Count"`
				HealthError   *int64 `json:"table6Count"`
			}
			if err := c.request(ctx, "cruddb", dbRead("StatisticsJSONDB", "getById", "statistics"), &raw); err != nil {
				return nil, err
			}
			if raw.Total == nil || *raw.Total < 0 {
				return nil, errors.New("Tdarr returned incompatible statistics")
			}
			out.TotalFiles = *raw.Total
			out.Transcodes = []Count{{"Queued", raw.Queued}, {"Success / not required", raw.Success}, {"Errors / cancelled", raw.Error}, {"Held", raw.Hold}}
			out.HealthChecks = []Count{{"Queued", raw.HealthQueued}, {"Success", raw.HealthSuccess}, {"Errors / cancelled", raw.HealthError}}
		} else {
			out.Note = "Tdarr file statuses may still say Queued while a file is processing or awaiting acceptance. Check Activity for running workers."
			type slice struct {
				Name  string `json:"name"`
				Value *int64 `json:"value"`
			}
			var raw struct {
				Pie *struct {
					Total  *int64 `json:"totalFiles"`
					Status struct {
						Transcodes   []slice `json:"transcode"`
						HealthChecks []slice `json:"healthcheck"`
					} `json:"status"`
				} `json:"pieStats"`
			}
			if err := c.request(ctx, "stats/get-pies", map[string]any{"data": map[string]string{"libraryId": libraryID}}, &raw); err != nil {
				return nil, err
			}
			if raw.Pie == nil || raw.Pie.Total == nil || *raw.Pie.Total < 0 || raw.Pie.Status.Transcodes == nil || raw.Pie.Status.HealthChecks == nil {
				return nil, errors.New("Tdarr returned incompatible library statistics")
			}
			out.TotalFiles = *raw.Pie.Total
			for _, v := range raw.Pie.Status.Transcodes {
				out.Transcodes = append(out.Transcodes, Count{v.Name, v.Value})
			}
			for _, v := range raw.Pie.Status.HealthChecks {
				out.HealthChecks = append(out.HealthChecks, Count{v.Name, v.Value})
			}
		}
		for _, list := range [][]Count{out.Transcodes, out.HealthChecks} {
			for _, v := range list {
				if v.Label == "" || (v.Value != nil && *v.Value < 0) {
					return nil, errors.New("Tdarr returned incompatible status counts")
				}
			}
		}
		out.ObservedAt = c.now().UTC()
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Stats), nil
}

func (c *Client) Validate(ctx context.Context) error {
	var status struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := c.request(ctx, "status", nil, &status); err != nil {
		return err
	}
	if status.Status != "good" || status.Version == "" {
		return errors.New("Tdarr returned an incompatible server status")
	}
	if _, err := c.Activity(ctx); err != nil {
		return err
	}
	libs, err := c.Libraries(ctx)
	if err != nil {
		return err
	}
	if _, err = c.Stats(ctx, ""); err != nil {
		return err
	}
	if len(libs.Items) > 0 {
		_, err = c.Stats(ctx, libs.Items[0].ID)
	}
	return err
}
