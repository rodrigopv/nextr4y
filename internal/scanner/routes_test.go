package scanner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrefixedAppAndPagesDeployment(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.URL.Path)
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("X-Vercel-Id", "example")
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/f/_next/static/chunks/runtime.js"></script>`+flightHTML("1:I[9,[\"21\",\"static/chunks/runtime.js\"],\"default\"]\n", "0:{\"b\":\"build\",\"p\":\"/f\",\"f\":[]}\n"))
		case "/f/_next/static/build/_buildManifest.js":
			fmt.Fprint(w, `self.__BUILD_MANIFEST={"/sign-in":["static/chunks/pages/sign-in-abc.js"],"/goals/[id]":["static/chunks/pages/goals/[id]-def.js"],__routerFilterStatic:{numItems:141},sortedPages:["/_app","/sign-in","/goals/[id]"]};`)
		case "/f/_next/static/chunks/runtime.js":
			fmt.Fprint(w, `window.next={version:"15.5.15",appDir:!0};self.webpackChunk_N_E=[];f.u=e=>"static/chunks/"+e+"."+({21:"f0cc1801",89:"9c9fef13"})[e]+".js",f.p="/f/_next/";`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()
	result, e := scannerFor().ScanTarget(srv.URL)
	require.NoError(t, e)
	require.Equal(t, "complete", result.ScanStatus)
	require.Empty(t, result.Warnings)
	require.Equal(t, "/f", result.AssetPrefix)
	require.Equal(t, srv.URL+"/f", result.AssetBaseURL)
	require.Equal(t, "mixed", result.Router)
	require.ElementsMatch(t, []string{"cloudflare", "vercel"}, result.ServingPlatforms)
	require.Len(t, result.Routes, 2)
	require.Len(t, result.ManifestAssets, 2)
	require.Len(t, result.ChunkMaps, 1)
	require.Len(t, result.ChunkMaps[0].Chunks, 2)
	require.Len(t, result.PageObservations, 1)
	require.Equal(t, []string{srv.URL + "/f/_next/static/chunks/runtime.js"}, result.PageObservations[0].Assets)
	require.Equal(t, "build", result.PageObservations[0].BuildID)
	require.Equal(t, []string{srv.URL + "/f/_next/static/build/_buildManifest.js"}, result.RouteSources["/sign-in"])
	// No speculative root URLs, no route visits, and no async inventory downloads.
	require.ElementsMatch(t, []string{"/", "/f/_next/static/build/_buildManifest.js", "/f/_next/static/chunks/runtime.js"}, requests)
}
func TestFlightPrefixBeforeImportsAndExplicitCDNPaths(t *testing.T) {
	base, _ := url.Parse("https://example.com/marketing")
	origin, _ := url.Parse("https://example.com")
	r := &ScanResult{AllAssets: map[string]bool{}}
	r.inspectFlight("2:I[1,[\"42\",\"static/chunks/a.js?dpl=keep\",\"https://other.example/_next/static/chunks/b.js\"],\"\"]\n0:{\"f\":[],\"p\":\"https://cdn.example/prefix\"}\n", base, origin, false, "inline-flight")
	require.True(t, r.AllAssets["https://cdn.example/prefix/_next/static/chunks/a.js?dpl=keep"])
	require.True(t, r.AllAssets["https://other.example/_next/static/chunks/b.js"])
	require.Len(t, r.AllAssets, 2)
}
func TestFlightReferencesAreNotPrefixes(t *testing.T) {
	base, _ := url.Parse("https://example.com")
	for _, prefix := range []string{"$undefined", "$L7"} {
		r := &ScanResult{AllAssets: map[string]bool{}}
		r.inspectFlight(fmt.Sprintf("0:{\"f\":[],\"p\":%q}\n1:I[1,[\"static/chunks/a.js\"],\"\"]\n", prefix), base, base, false, "inline-flight")
		require.Empty(t, r.AssetPrefix)
		require.True(t, r.AllAssets["https://example.com/_next/static/chunks/a.js"])
	}
}
func TestBoundedPageObservationsStaySeparateFromManifestRoutes(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<a href="/a">A</a><a href="/b">B</a>`)
		case "/a":
			fmt.Fprint(w, `<a href="/c">C</a><script src="/zone/_next/static/chunks/a.js"></script>`+flightHTML("0:{\"b\":\"other-build\",\"p\":\"/zone\",\"f\":[]}\n"))
		default:
			fmt.Fprint(w, "other")
		}
	}))
	defer srv.Close()
	s := scannerFor()
	s.Options.CrawlPages = 1
	r, e := s.ScanTarget(srv.URL)
	require.NoError(t, e)
	require.Len(t, r.PageObservations, 2)
	require.Equal(t, "other-build", r.PageObservations[1].BuildID)
	require.Equal(t, srv.URL+"/a", r.PageObservations[1].FinalURL)
	require.Empty(t, r.BuildID)
	require.Empty(t, r.Routes)
	require.Empty(t, r.AssetPrefix)
	require.Equal(t, []string{"/", "/a"}, requests)
	for _, u := range r.DiscoveredURLs {
		require.False(t, strings.Contains(u, "other-build"))
	}
}
