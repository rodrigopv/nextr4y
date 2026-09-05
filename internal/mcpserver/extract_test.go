package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractionToolsWorkflow(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `self.webpackChunk_N_E.push([[1],{2:(e,t,n)=>{t.message="héllo"}}]);`)
	}))
	defer site.Close()
	s := NewMCPServer("127.0.0.1", 0)
	require.NoError(t, s.InitMCPServer())
	result := rpc(t, s, "tools/call", map[string]any{"name": "nextr4y_extract_routes", "arguments": map[string]any{"scan": map[string]any{"Routes": map[string]any{"/back/[id]": []string{site.URL + "/page.js"}}}, "routes": []string{"/back/[id]"}}})
	require.NotEqual(t, true, result["isError"])
	var extracted map[string]any
	require.NoError(t, json.Unmarshal([]byte(result["content"].([]any)[0].(map[string]any)["text"].(string)), &extracted))
	defer os.RemoveAll(extracted["directory"].(string))
	require.Equal(t, "complete", extracted["status"])
	id := extracted["extraction_id"]
	read := rpc(t, s, "tools/call", map[string]any{"name": "nextr4y_read_extraction", "arguments": map[string]any{"extraction_id": id, "limit": 25}})
	require.NotEqual(t, true, read["isError"])
	var section map[string]any
	require.NoError(t, json.Unmarshal([]byte(read["content"].([]any)[0].(map[string]any)["text"].(string)), &section))
	require.Equal(t, float64(25), section["next_offset"])
	denied := rpc(t, s, "tools/call", map[string]any{"name": "nextr4y_read_extraction", "arguments": map[string]any{"extraction_id": id, "file": "../../go.mod"}})
	require.Equal(t, true, denied["isError"])
}
