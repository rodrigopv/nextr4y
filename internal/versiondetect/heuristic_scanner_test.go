package versiondetect

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/stretchr/testify/require"
)

type fixtureFetcher struct {
	bodies map[string]string
	calls  map[string]int
}

func (f *fixtureFetcher) Capabilities() fetch.FetcherCapabilities { return fetch.FetcherCapabilities{} }
func (f *fixtureFetcher) Fetch(u string) (io.ReadCloser, string, error) {
	f.calls[u]++
	if body, ok := f.bodies[u]; ok {
		return io.NopCloser(strings.NewReader(body)), u, nil
	}
	return nil, u, fmt.Errorf("missing fixture")
}
func TestObservedDeployments(t *testing.T) {
	files, e := filepath.Glob("testdata/*.json")
	require.NoError(t, e)
	require.Len(t, files, 5)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var fixture struct {
				Next, React, Bundler string
				Chunks               map[string]string
			}
			data, e := os.ReadFile(file)
			require.NoError(t, e)
			require.NoError(t, json.Unmarshal(data, &fixture))
			f := &fixtureFetcher{fixture.Chunks, map[string]int{}}
			urls := map[string]bool{}
			for u := range fixture.Chunks {
				urls[u] = true
			}
			report := (&HeuristicAssetScannerDetector{}).DetectEvidence(urls, f)
			for property, want := range map[string]string{"next-version": fixture.Next, "react-version": fixture.React, "bundler": fixture.Bundler, "router": "app"} {
				value, conflict := Resolve(report.Findings, property)
				require.False(t, conflict)
				require.Equal(t, want, value)
			}
			for _, count := range f.calls {
				require.Equal(t, 1, count)
			}
		})
	}
}
func TestUnattributedVersionsNeverIdentifyNext(t *testing.T) {
	f := &fixtureFetcher{map[string]string{"asset.js": `r.version="19.3.0-canary-test";({version:"3.38.1",mode:"global"})`}, map[string]int{}}
	report := (&HeuristicAssetScannerDetector{}).DetectEvidence(map[string]bool{"asset.js": true}, f)
	value, conflict := Resolve(report.Findings, "next-version")
	require.Equal(t, "Unknown", value)
	require.False(t, conflict)
	require.NotEmpty(t, report.Findings)
}
func TestConflictsDoNotDependOnOrder(t *testing.T) {
	a := finding("next-version", "16.1.6", "one", "a", "")
	b := finding("next-version", "16.0.10", "two", "b", "")
	for _, findings := range [][]Finding{{a, b}, {b, a}} {
		v, c := Resolve(findings, "next-version")
		require.Equal(t, "Unknown", v)
		require.True(t, c)
	}
}
func TestTechniquesRemainAdditive(t *testing.T) {
	f := &fixtureFetcher{map[string]string{"ok": `window.next={version:"16.1.6",appDir:!0}`}, map[string]int{}}
	called := false
	techniques := append(DefaultTechniques(), AssetTechnique{"custom", func(u, b string) []Finding {
		called = true
		return []Finding{finding("custom", "present", "custom", u, "")}
	}})
	r := (&HeuristicAssetScannerDetector{Techniques: techniques}).DetectEvidence(map[string]bool{"missing": true, "ok": true}, f)
	require.True(t, called)
	require.Len(t, r.Warnings, 1)
	v, _ := Resolve(r.Findings, "next-version")
	require.Equal(t, "16.1.6", v)
}
func TestVariableBindingIsLocal(t *testing.T) {
	require.Len(t, inspectNextConstant("u", `const v="15.2.0";window.next={version:v,appDir:!0}`), 1)
	require.Empty(t, inspectNextConstant("u", `const other="3.38.1";window.next={version:v,appDir:!0}`))
	require.Empty(t, inspectNextConstant("u", `const v="3.38.1";function nested(){window.next={version:v}}`))
}

func TestWebpackCompatibilityCodeIsNotTurbopack(t *testing.T) {
	result := inspectBundler("webpack.js", `self.webpackChunk_N_E.push([]); if(window.next.turbopack){console.log("TURBOPACK")}`)
	require.Len(t, result, 1)
	require.Equal(t, "webpack", result[0].Value)
}
