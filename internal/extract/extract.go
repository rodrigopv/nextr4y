// Package extract collects public client bundles without executing them or visiting routes.
package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rodrigopv/nextr4y/internal/chunkmap"
	"github.com/rodrigopv/nextr4y/internal/fetch"
)

type Input struct {
	BaseURL, BuildID string
	Routes           map[string][]string
	ChunkMaps        []chunkmap.Inventory
}
type Options struct {
	MaxAssets, MaxDepth int
	FollowLazy          bool
}
type Edge struct{ From, ModuleID, ChunkID, URL, Status string }
type Asset struct {
	URL, FinalURL, File, SHA256, Status, Error, ContentType string
	HTTPStatus, Bytes, Depth                                int
	Routes                                                  []string
	Analysis                                                *Analysis `json:",omitempty"`
}
type Result struct {
	Directory, IndexFile, Status, BaseURL, BuildID string
	Routes                                         map[string][]string
	Assets                                         []*Asset
	Dependencies                                   []Edge
	Warnings                                       []string
	Bytes                                          int
}

func httpURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Fragment == ""
}
func Validate(in Input, routes []string, o Options) error {
	if len(routes) == 0 || len(routes) > 32 {
		return fmt.Errorf("select 1–32 exact manifest route patterns")
	}
	if o.MaxAssets < 1 || o.MaxAssets > 128 || o.MaxDepth < 0 || o.MaxDepth > 4 {
		return fmt.Errorf("max_assets must be 1–128 and max_depth 0–4")
	}
	for _, r := range routes {
		assets, ok := in.Routes[r]
		if !ok || len(assets) == 0 {
			return fmt.Errorf("route %q has no manifest assets in supplied scan", r)
		}
		if len(assets) > 1024 {
			return fmt.Errorf("route asset list exceeds 1024 entries")
		}
		for _, a := range assets {
			if !httpURL(a) {
				return fmt.Errorf("invalid asset URL for %q", r)
			}
		}
	}
	return nil
}
func Run(ctx context.Context, in Input, routes []string, o Options, f fetch.Fetcher, parent string) (*Result, error) {
	if err := Validate(in, routes, o); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "nextr4y-extract-")
	if err != nil {
		return nil, err
	}
	r := &Result{Directory: dir, IndexFile: "index.json", Status: "complete", BaseURL: in.BaseURL, BuildID: in.BuildID, Routes: map[string][]string{}}
	r.Warnings = append(r.Warnings, "Client bundles only; module references are static candidates, not proof of execution or complete dependency closure. Source maps and server logic are not recovered.")
	queue := []*Asset{}
	seen := map[string]*Asset{}
	enqueue := func(raw, route string, depth int) bool {
		if a := seen[raw]; a != nil {
			if route != "" {
				a.Routes = unique(append(a.Routes, route))
			}
			return true
		}
		if len(queue) >= o.MaxAssets {
			return false
		}
		a := &Asset{URL: raw, Depth: depth, Status: "pending"}
		if route != "" {
			a.Routes = []string{route}
		}
		seen[raw] = a
		queue = append(queue, a)
		return true
	}
	for _, route := range unique(routes) {
		r.Routes[route] = unique(in.Routes[route])
		for _, raw := range r.Routes[route] {
			if !enqueue(raw, route, 0) {
				r.Status = "partial"
				r.Warnings = append(r.Warnings, "Asset limit reached; some manifest assets were not fetched")
			}
		}
	}
	// Inventories are scoped by their runtime's public path. Conflicting IDs stay unresolved.
	resolve := func(from, id string) []string {
		var candidates []string
		for _, inv := range in.ChunkMaps {
			if inv.PublicPath == "" || !strings.HasPrefix(from, strings.TrimRight(inv.PublicPath, "/")+"/") {
				continue
			}
			if raw := inv.Chunks[id]; httpURL(raw) {
				candidates = append(candidates, raw)
			}
		}
		return unique(candidates)
	}
	for i := 0; i < len(queue); i++ {
		a := queue[i]
		r.Assets = append(r.Assets, a)
		if ctx.Err() != nil {
			a.Status = "cancelled"
			r.Status = "partial"
			continue
		}
		if r.Bytes >= 64<<20 {
			a.Status = "byte_limit"
			r.Status = "partial"
			continue
		}
		log.Printf("Extract [%d/%d]: %s", i+1, o.MaxAssets, a.URL)
		res, e := fetch.Read(f, fetch.Request{URL: a.URL})
		if res != nil {
			a.FinalURL = res.URL
			a.HTTPStatus = res.StatusCode
			a.ContentType = res.Headers.Get("Content-Type")
		}
		if e != nil {
			a.Status = "fetch_failed"
			a.Error = e.Error()
			r.Status = "partial"
			continue
		}
		if res.StatusCode != 200 {
			a.Status = "http_error"
			r.Status = "partial"
			continue
		}
		if len(res.Body) > fetch.MaxBodyBytes || r.Bytes+len(res.Body) > 64<<20 {
			a.Status = "byte_limit"
			r.Status = "partial"
			continue
		}
		// A login or challenge page is not the requested JS/CSS bundle.
		parsed, _ := url.Parse(a.URL)
		if (strings.HasSuffix(parsed.Path, ".js") || strings.HasSuffix(parsed.Path, ".css")) && (strings.Contains(strings.ToLower(a.ContentType), "text/html") || strings.HasPrefix(strings.TrimSpace(string(res.Body)), "<!DOCTYPE html")) {
			a.Status = "unexpected_html"
			r.Status = "partial"
			continue
		}
		a.Bytes = len(res.Body)
		r.Bytes += a.Bytes
		hash := sha256.Sum256(res.Body)
		a.SHA256 = hex.EncodeToString(hash[:])
		// Names depend only on a digest, never remote paths or route parameters.
		urlHash := sha256.Sum256([]byte(a.URL))
		prefix := hex.EncodeToString(urlHash[:])
		ext := filepath.Ext(parsed.Path)
		if ext != ".js" && ext != ".css" {
			ext = ".bin"
		}
		a.File = prefix + ext
		if err := os.WriteFile(filepath.Join(dir, a.File), res.Body, 0600); err != nil {
			a.Status = "write_failed"
			a.Error = err.Error()
			a.File = ""
			r.Status = "partial"
			continue
		}
		a.Status = "saved"
		if ext == ".js" {
			analysis := analyze(res.Body, func(name string, data []byte) error {
				return os.WriteFile(filepath.Join(dir, prefix+"-"+name), data, 0600)
			})
			for j := range analysis.Modules {
				if analysis.Modules[j].File != "" {
					analysis.Modules[j].File = prefix + "-" + analysis.Modules[j].File
				}
			}
			a.Analysis = &analysis
			for _, m := range analysis.Modules {
				if m.UnresolvedLazyCalls > 0 {
					r.Dependencies = append(r.Dependencies, Edge{From: a.URL, ModuleID: m.ID, Status: "dynamic_expression"})
				}
				for _, id := range m.LazyChunkIDs {
					edge := Edge{From: a.URL, ModuleID: m.ID, ChunkID: id, Status: "unresolved"}
					candidates := resolve(a.FinalURL, id)
					if len(candidates) > 1 {
						edge.Status = "ambiguous"
					} else if len(candidates) == 1 {
						edge.URL = candidates[0]
						edge.Status = "not_followed"
						if o.FollowLazy {
							if seen[edge.URL] != nil {
								edge.Status = "queued"
							} else if a.Depth >= o.MaxDepth {
								edge.Status = "depth_limit"
								r.Status = "partial"
							} else if enqueue(edge.URL, "", a.Depth+1) {
								edge.Status = "queued"
							} else {
								edge.Status = "asset_limit"
								r.Status = "partial"
							}
						}
					}
					r.Dependencies = append(r.Dependencies, edge)
				}
			}
		}
	}
	for i := range r.Dependencies {
		e := &r.Dependencies[i]
		if e.Status == "queued" {
			e.Status = seen[e.URL].Status
		}
	}
	r.Warnings = unique(r.Warnings)
	sort.Slice(r.Assets, func(i, j int) bool { return r.Assets[i].URL < r.Assets[j].URL })
	data, err := json.MarshalIndent(r, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, r.IndexFile), data, 0600)
	}
	return r, err
}
