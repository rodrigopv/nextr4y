package extract

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rodrigopv/nextr4y/internal/chunkmap"
	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	bodies map[string]string
	calls  []string
}

func (f *fixture) Fetch(u string) (io.ReadCloser, string, error) { return nil, u, fmt.Errorf("unused") }
func (f *fixture) Capabilities() fetch.FetcherCapabilities       { return fetch.FetcherCapabilities{} }
func (f *fixture) FetchResponse(req fetch.Request) (*fetch.Response, error) {
	f.calls = append(f.calls, req.URL)
	body, ok := f.bodies[req.URL]
	status := 200
	if !ok {
		status = 404
	}
	return &fetch.Response{URL: req.URL, StatusCode: status, Headers: http.Header{}, Body: []byte(body)}, nil
}
func TestExtractModulesDependenciesAndDedup(t *testing.T) {
	root := "https://example.com/f/_next/"
	entry := root + "static/chunks/pages/front.js"
	shared := root + "static/chunks/shared.js"
	lazy := root + "static/chunks/21.abc.js"
	f := &fixture{bodies: map[string]string{
		entry:  `(self.webpackChunk_N_E=self.webpackChunk_N_E||[]).push([[1],{10:(e,t,n)=>{n(11);n.e(21);n.e(variable);const a="n.e(999)";fetch("/api/example")}}]);`,
		shared: `(self.webpackChunk_N_E=self.webpackChunk_N_E||[]).push([[2],{11:function(e,t,n){t.x="hello"}}]);`,
		lazy:   `(self.webpackChunk_N_E=self.webpackChunk_N_E||[]).push([[21],{12:(e,t,n)=>{n.e(21)}}]);`,
	}}
	in := Input{Routes: map[string][]string{"/front": {entry, shared}, "/back/[id]": {shared}}, ChunkMaps: []chunkmap.Inventory{{PublicPath: root, Chunks: map[string]string{"21": lazy}}}}
	result, err := Run(context.Background(), in, []string{"/front", "/back/[id]"}, Options{MaxAssets: 8, MaxDepth: 2, FollowLazy: true}, f, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "complete", result.Status)
	require.ElementsMatch(t, []string{entry, shared, lazy}, f.calls)
	require.Len(t, result.Assets, 3)
	for _, a := range result.Assets {
		require.Equal(t, "saved", a.Status)
		require.Len(t, a.SHA256, 64)
		raw, err := os.ReadFile(filepath.Join(result.Directory, a.File))
		require.NoError(t, err)
		require.Equal(t, f.bodies[a.URL], string(raw))
		require.Len(t, a.Analysis.Modules, 1)
		m := a.Analysis.Modules[0]
		content, err := os.ReadFile(filepath.Join(result.Directory, m.File))
		require.NoError(t, err)
		require.Equal(t, string(raw[m.StartByte:m.EndByte]), string(content))
		if a.URL == entry {
			require.Equal(t, []string{"11"}, m.Requires)
			require.Equal(t, []string{"21"}, m.LazyChunkIDs)
			require.Equal(t, 1, m.UnresolvedLazyCalls)
			require.Contains(t, a.Analysis.References, "/api/example")
		}
		if a.URL == shared {
			require.ElementsMatch(t, []string{"/front", "/back/[id]"}, a.Routes)
		}
	}
	require.FileExists(t, filepath.Join(result.Directory, "index.json"))
}
func TestLimitsAndPartialFailures(t *testing.T) {
	root := "https://example.com/_next/"
	entry := root + "entry.js"
	f := &fixture{bodies: map[string]string{entry: `self.webpackChunk_N_E.push([[1],{1:(e,t,n)=>n.e(2)}]);`}}
	in := Input{Routes: map[string][]string{"/a": {entry}}, ChunkMaps: []chunkmap.Inventory{{PublicPath: root, Chunks: map[string]string{"2": root + "missing.js"}}}}
	r, e := Run(context.Background(), in, []string{"/a"}, Options{MaxAssets: 4, MaxDepth: 0, FollowLazy: true}, f, t.TempDir())
	require.NoError(t, e)
	require.Equal(t, "partial", r.Status)
	require.Len(t, f.calls, 1)
	require.Equal(t, "depth_limit", r.Dependencies[0].Status)
	f.calls = nil
	r, e = Run(context.Background(), in, []string{"/a"}, Options{MaxAssets: 4, MaxDepth: 2, FollowLazy: true}, f, t.TempDir())
	require.NoError(t, e)
	require.Equal(t, "partial", r.Status)
	require.Len(t, f.calls, 2)
	require.Equal(t, "http_error", r.Dependencies[0].Status)
	f.calls = nil
	_, e = Run(context.Background(), in, []string{"/unknown"}, Options{MaxAssets: 4}, f, t.TempDir())
	require.Error(t, e)
	require.Empty(t, f.calls)
}
func TestDefaultDoesNotFollowAndAmbiguousMap(t *testing.T) {
	f := &fixture{bodies: map[string]string{"https://example.com/a.js": `self.webpackChunk_N_E.push([[1],{1:(e,t,n)=>n.e(2)}]);`}}
	in := Input{Routes: map[string][]string{"/a": {"https://example.com/a.js"}}, ChunkMaps: []chunkmap.Inventory{{PublicPath: "https://example.com/", Chunks: map[string]string{"2": "https://example.com/b.js"}}}}
	r, e := Run(context.Background(), in, []string{"/a"}, Options{MaxAssets: 4, MaxDepth: 2}, f, t.TempDir())
	require.NoError(t, e)
	require.Len(t, f.calls, 1)
	require.Equal(t, "not_followed", r.Dependencies[0].Status)
	in.ChunkMaps = append(in.ChunkMaps, chunkmap.Inventory{PublicPath: "https://example.com/", Chunks: map[string]string{"2": "https://example.com/c.js"}})
	r, e = Run(context.Background(), in, []string{"/a"}, Options{MaxAssets: 4, MaxDepth: 2, FollowLazy: true}, f, t.TempDir())
	require.NoError(t, e)
	require.Len(t, f.calls, 2)
	require.Equal(t, "ambiguous", r.Dependencies[0].Status)
}
