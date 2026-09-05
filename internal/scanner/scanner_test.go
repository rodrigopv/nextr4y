package scanner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/rodrigopv/nextr4y/internal/versiondetect"
	"github.com/stretchr/testify/require"
)

func scannerFor() *Scanner {
	return NewScanner(fetch.NewHTTPFetcher(), &versiondetect.HeuristicAssetScannerDetector{}, "")
}
func flightHTML(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		data, _ := json.Marshal([]interface{}{1, part})
		fmt.Fprintf(&b, "<script>self.__next_f.push(%s)</script>", data)
	}
	return b.String()
}
func TestAppRouterFlightAndHeaders(t *testing.T) {
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `window.next={version:"16.3.2",appDir:!0};window.next.turbopack=!0`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Opennext", "1")
		w.Header().Set("Server", "cloudflare")
		fmt.Fprint(w, flightHTML("0:{\"b\":\"build", "-123\",\"f\":[]}\n1:I[123,[\"/_next/static/chunks/runtime.js?dpl=dpl_test\"],\"default\"]\n"))
	}))
	defer srv.Close()
	r, e := scannerFor().ScanTarget(srv.URL)
	require.NoError(t, e)
	require.Equal(t, "complete", r.ScanStatus)
	require.True(t, r.IsNextJS)
	require.Equal(t, "app", r.Router)
	require.Equal(t, "16.3.2", r.DetectedNextVersion)
	require.Equal(t, "build-123", r.BuildID)
	require.Equal(t, "dpl_test", r.DeploymentID)
	require.Equal(t, "opennext", r.Adapter)
	require.Equal(t, "cloudflare", r.Platform)
	require.Equal(t, "unavailable", r.ManifestStatus)
	require.Len(t, requests, 3)
}
func TestPagesManifestCompatibility(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nested/page":
			fmt.Fprint(w, `<script id="__NEXT_DATA__" type="application/json">{"buildId":"build","props":{},"assetPrefix":"/prefix"}</script>`)
		case "/prefix/_next/static/build/_buildManifest.js":
			fmt.Fprint(w, `self.__BUILD_MANIFEST={"/": ["static/chunks/page.js"]};self.__BUILD_MANIFEST_CB&&self.__BUILD_MANIFEST_CB()`)
		case "/prefix/_next/static/chunks/page.js":
			fmt.Fprint(w, `window.next={version:"12.3.4"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	r, e := scannerFor().ScanTarget(srv.URL + "/nested/page")
	require.NoError(t, e)
	require.Equal(t, "pages", r.Router)
	require.True(t, r.ManifestExecOK)
	require.Equal(t, []string{srv.URL + "/prefix/_next/static/chunks/page.js"}, r.Routes["/"])
	require.Equal(t, "12.3.4", r.DetectedNextVersion)
}
func TestUnknownVersusNotDetected(t *testing.T) {
	for _, status := range []int{200, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, "ordinary page") }))
			defer srv.Close()
			r, e := scannerFor().ScanTarget(srv.URL)
			require.NoError(t, e)
			encoded, e := json.Marshal(r)
			require.NoError(t, e)
			var data map[string]interface{}
			require.NoError(t, json.Unmarshal(encoded, &data))
			if status == 200 {
				require.Equal(t, "not_detected", r.DetectionStatus)
				require.Equal(t, false, data["IsNextJS"])
			} else {
				require.Equal(t, "unknown", r.DetectionStatus)
				require.Nil(t, data["IsNextJS"])
			}
		})
	}
}
func Test404StillIdentifiesNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Powered-By", "Next.js")
		w.WriteHeader(404)
	}))
	defer srv.Close()
	r, e := scannerFor().ScanTarget(srv.URL)
	require.NoError(t, e)
	require.True(t, r.IsNextJS)
	require.Equal(t, "partial", r.ScanStatus)
}
func TestFailedTransportSerializesError(t *testing.T) {
	r, e := scannerFor().ScanTarget("ftp://example.com")
	require.Error(t, e)
	data, e := json.Marshal(r)
	require.NoError(t, e)
	require.Contains(t, string(data), `"IsNextJS":null`)
	require.Contains(t, string(data), `"ExecutionError":"invalid HTTP URL`)
	require.Contains(t, FormatText(r), "Execution Error:")
}
func TestAssetPrefixesAndQueries(t *testing.T) {
	base, _ := url.Parse("https://example.com/nested/page")
	override, _ := url.Parse("https://cdn.example.com/assets")
	r := &ScanResult{AllAssets: map[string]bool{}}
	r.inspectHTML(`<link rel="preload" as="script" href="/vc-ap-marketing/_next/static/immutable/chunks/a.js?dpl=123"><script src="https://other.example.com/prefix/_next/static/chunks/b.js"></script>`, base, override, false)
	require.True(t, r.AllAssets["https://example.com/vc-ap-marketing/_next/static/immutable/chunks/a.js?dpl=123"])
	require.Contains(t, r.ObservedAssetPrefixes, "/vc-ap-marketing")
	require.Contains(t, r.ObservedAssetPrefixes, "https://other.example.com/prefix")
	r.asset("/prefix/_next/static/chunks/c.js?dpl=456", base, override, true)
	require.True(t, r.AllAssets["https://cdn.example.com/assets/_next/static/chunks/c.js?dpl=456"])
}
func TestRSCAndSitemapAreOptionalAndIndependent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("RSC") == "1" {
			w.Header().Set("Content-Type", "text/x-component")
			fmt.Fprint(w, "0:{\"b\":\"rsc-build\",\"f\":[]}\n")
			return
		}
		if r.URL.Path == "/sitemap.xml" {
			fmt.Fprintf(w, `<urlset><url><loc>http://%s/article</loc></url><url><loc>https://external.example/no</loc></url></urlset>`, r.Host)
			return
		}
		fmt.Fprint(w, `<a href="/about">About</a>`)
	}))
	defer srv.Close()
	s := scannerFor()
	s.Options = Options{ProbeRSC: true, DiscoverRoutes: true}
	r, e := s.ScanTarget(srv.URL)
	require.NoError(t, e)
	require.Equal(t, "app", r.Router)
	require.Equal(t, "rsc-build", r.BuildID)
	require.ElementsMatch(t, []string{srv.URL + "/about", srv.URL + "/article"}, r.DiscoveredURLs)
}
func TestMalformedFlightDoesNotExecuteScripts(t *testing.T) {
	base, _ := url.Parse("https://example.com")
	r := &ScanResult{AllAssets: map[string]bool{}}
	r.inspectHTML(`<script>self.__next_f.push((function(){while(true){}})())</script>`+flightHTML("0:{not-json}\n"), base, base, false)
	require.Empty(t, r.Findings)
}
func TestManifestExecutionBounded(t *testing.T) {
	_, e := executeManifestJS(`self.__BUILD_MANIFEST=(function(){while(true){}})()`)
	require.Error(t, e)
	require.Contains(t, e.Error(), "timed out")
}

func TestLegacyFlightBuildID(t *testing.T) {
	base, _ := url.Parse("https://doge.gov")
	r := &ScanResult{AllAssets: map[string]bool{}}
	r.inspectFlight(`0:[[],["$","$L3",null,{"buildId":"6Hltilj9A9q8WMX2jIc25","assetPrefix":"","initialTree":["",{}],"initialSeedData":[]}]]`, base, base, false, "inline-flight")
	r.resolve()
	require.True(t, r.IsNextJS)
	require.Equal(t, "app", r.Router)
	require.Equal(t, "6Hltilj9A9q8WMX2jIc25", r.BuildID)
	unrelated := &ScanResult{AllAssets: map[string]bool{}}
	unrelated.inspectFlight(`0:["$","div",null,{"buildId":"not-a-build"}]`, base, base, false, "inline-flight")
	require.Empty(t, unrelated.Findings)
}

func TestCustomBasePreservesDeclaredPrefix(t *testing.T) {
	var assetPath string
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "_buildManifest.js") {
			fmt.Fprint(w, `self.__BUILD_MANIFEST={"/":["static/chunks/page.js?dpl=keep"]}`)
			return
		}
		assetPath = r.URL.RequestURI()
		fmt.Fprint(w, `window.next={version:"12.3.4"}`)
	}))
	defer cdn.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<script id="__NEXT_DATA__">{"buildId":"test","props":{},"assetPrefix":"/prefix"}</script>`)
	}))
	defer site.Close()
	s := NewScanner(fetch.NewHTTPFetcher(), &versiondetect.HeuristicAssetScannerDetector{}, cdn.URL+"/base")
	r, e := s.ScanTarget(site.URL)
	require.NoError(t, e)
	require.True(t, r.ManifestExecOK)
	require.Equal(t, "/base/prefix/_next/static/chunks/page.js?dpl=keep", assetPath)
	require.Equal(t, "12.3.4", r.DetectedNextVersion)
}
