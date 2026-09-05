package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func exerciseClient(t *testing.T, c *client.Client, target, version string) {
	t.Helper()
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = version
	req.Params.ClientInfo = mcp.Implementation{Name: "nextr4y-integration", Version: "1"}
	initialized, err := c.Initialize(ctx, req)
	require.NoError(t, err)
	if version != "" {
		require.Equal(t, version, initialized.ProtocolVersion)
	}
	require.Contains(t, initialized.Instructions, "ChunkMaps")
	listed, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 3)
	names := []string{}
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "nextr4y_scan")
	call := mcp.CallToolRequest{}
	call.Params.Name = "nextr4y_scan"
	call.Params.Arguments = map[string]any{"url": target, "format": "json"}
	result, err := c.CallTool(ctx, call)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	content, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var scan map[string]any
	require.NoError(t, json.Unmarshal([]byte(content.Text), &scan))
	require.Equal(t, "complete", scan["ScanStatus"])
	require.Equal(t, "15.5.15", scan["DetectedNextVersion"])
	require.Len(t, scan["PageObservations"], 1)
}

func TestHTTPAndStdioTransports(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_next/static/chunks/runtime.js" {
			fmt.Fprint(w, `window.next={version:"15.5.15",appDir:!0};`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<script src="/_next/static/chunks/runtime.js"></script>`)
	}))
	defer target.Close()
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	endpoint := httptest.NewServer(s.HTTPHandler())
	defer endpoint.Close()
	for _, version := range []string{"2025-11-25", ""} {
		t.Run("http/"+version, func(t *testing.T) {
			c, err := client.NewStreamableHttpClient(endpoint.URL + "/mcp")
			require.NoError(t, err)
			require.NoError(t, c.Start(t.Context()))
			exerciseClient(t, c, target.URL, version)
		})
	}
	t.Run("legacy-sse", func(t *testing.T) {
		c, err := client.NewSSEMCPClient(endpoint.URL + "/sse")
		require.NoError(t, err)
		require.NoError(t, c.Start(t.Context()))
		exerciseClient(t, c, target.URL, "2024-11-05")
	})
	binary := filepath.Join(t.TempDir(), "nextr4y")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/nextr4y")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, version := range []string{"2025-11-25", ""} {
		t.Run("stdio/"+version, func(t *testing.T) {
			c, err := client.NewStdioMCPClient(binary, nil, "serve", "--transport", "stdio")
			require.NoError(t, err)
			exerciseClient(t, c, target.URL, version)
		})
	}
}

func TestHTTPRejectsForeignOrigin(t *testing.T) {
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	for _, path := range []string{"/mcp", "/sse", "/message"} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, nil)
		req.Header.Set("Origin", "https://foreign.example")
		w := httptest.NewRecorder()
		s.HTTPHandler().ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
	}
}
