package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func rpc(t *testing.T, s *MCPServer, method string, params any) map[string]any {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	response := s.mcpServer.HandleMessage(context.Background(), request)
	data, err := json.Marshal(response)
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(data, &envelope))
	require.Nil(t, envelope["error"], string(data))
	return envelope["result"].(map[string]any)
}
func call(t *testing.T, s *MCPServer, args map[string]any) (map[string]any, string) {
	t.Helper()
	result := rpc(t, s, "tools/call", map[string]any{"name": "nextr4y_scan", "arguments": args})
	return result, result["content"].([]any)[0].(map[string]any)["text"].(string)
}
func TestRegisteredSchemaAndValidation(t *testing.T) {
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	listed := rpc(t, s, "tools/list", map[string]any{})["tools"].([]any)
	require.Len(t, listed, 3)
	var tool map[string]any
	for _, item := range listed {
		candidate := item.(map[string]any)
		if candidate["name"] == "nextr4y_scan" {
			tool = candidate
		}
	}
	require.NotNil(t, tool)
	require.Contains(t, tool["description"], "ChunkMaps")
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"url", "format", "base_url", "insecure", "rsc", "sitemap", "crawl_pages"} {
		require.Contains(t, props, name)
	}
	crawl := props["crawl_pages"].(map[string]any)
	require.Equal(t, float64(0), crawl["minimum"])
	require.Equal(t, float64(32), crawl["maximum"])
	require.Equal(t, float64(1), crawl["multipleOf"])
	for _, bad := range []map[string]any{
		{"crawl_pages": -1}, {"crawl_pages": 33}, {"crawl_pages": 1.5}, {"crawl_pages": "2"},
		{"rsc": "true"}, {"sitemap": 1}, {"insecure": "false"}, {"format": "xml"}, {"base_url": true},
	} {
		bad["url"] = "http://127.0.0.1:1"
		result, _ := call(t, s, bad)
		require.Equal(t, true, result["isError"])
		_, err := s.RegisterScanTool().Handler(bad)
		require.Error(t, err)
	}
}
func TestRegisteredScanExposesRouteEvidenceAndOptions(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		if r.Header.Get("RSC") == "1" {
			requests["rsc"]++
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/":
			if r.Header.Get("RSC") == "1" {
				w.Header().Set("Content-Type", "text/x-component")
				fmt.Fprint(w, "0:{\"b\":\"build\",\"p\":\"/f\",\"f\":[]}\n")
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<a href="/observed">page</a><script src="/f/_next/static/chunks/runtime.js"></script><script>self.__next_f.push([1,"0:{\"b\":\"build\",\"p\":\"/f\",\"f\":[]}\n"])</script>`)
		case "/f/_next/static/build/_buildManifest.js":
			fmt.Fprint(w, `self.__BUILD_MANIFEST={"/sign-in":["static/chunks/pages/sign-in-abc.js"]};`)
		case "/f/_next/static/chunks/runtime.js":
			fmt.Fprint(w, `window.next={version:"15.5.15",appDir:!0};f.u=e=>"static/chunks/"+e+"."+({21:"abc"})[e]+".js",f.p="/f/_next/";`)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<urlset><url><loc>https://%s/from-sitemap</loc></url></urlset>`, r.Host)
		case "/observed":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/f/_next/static/chunks/observed.js"></script>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	result, text := call(t, s, map[string]any{"url": site.URL, "base_url": site.URL + "/f", "insecure": true, "rsc": true, "sitemap": true, "crawl_pages": 1})
	require.NotEqual(t, true, result["isError"], text)
	var scan map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &scan))
	require.Equal(t, true, scan["TLSVerificationDisabled"])
	require.Equal(t, "mixed", scan["Router"])
	require.Equal(t, "15.5.15", scan["DetectedNextVersion"])
	require.Len(t, scan["Routes"], 1)
	require.Len(t, scan["RouteSources"], 1)
	require.Len(t, scan["ManifestAssets"], 1)
	require.Len(t, scan["ChunkMaps"], 1)
	require.Len(t, scan["PageObservations"], 3)
	require.Contains(t, scan["DiscoveredURLs"], site.URL+"/from-sitemap")
	mu.Lock()
	require.Equal(t, 1, requests["rsc"])
	require.Equal(t, 1, requests["/observed"])
	require.Zero(t, requests["/sign-in"])
	require.Zero(t, requests["/f/_next/static/chunks/21.abc.js"])
	mu.Unlock()
	_, text = call(t, s, map[string]any{"url": site.URL + "/observed", "insecure": true, "format": "text"})
	require.Contains(t, text, "Page observations:")
	require.Contains(t, text, "Manifest route patterns:")
}
func TestPartialFailureRemainsJSON(t *testing.T) {
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	result, text := call(t, s, map[string]any{"url": "ftp://example.com"})
	require.Equal(t, true, result["isError"])
	var scan map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &scan))
	require.NotEmpty(t, scan["ExecutionError"])
	require.Nil(t, scan["IsNextJS"])
}
