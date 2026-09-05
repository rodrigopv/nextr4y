package scanner

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rodrigopv/nextr4y/internal/chunkmap"
	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/rodrigopv/nextr4y/internal/versiondetect"
)

type PageObservation struct {
	RequestedURL string
	FinalURL     string
	Technique    string
	HTTPStatus   int
	BuildID      string
	AssetPrefix  string
	Assets       []string
}
type ManifestObservation struct {
	URL        string
	HTTPStatus int
	Status     string
	Routes     int
}
type ScanResult struct {
	ChunkMaps               []chunkmap.Inventory
	PageObservations        []PageObservation
	Manifests               []ManifestObservation
	ManifestAssets          map[string]bool
	RouteSources            map[string][]string
	ServingPlatforms        []string
	observation             *PageObservation
	BaseURL                 string
	AssetBaseURL            string
	IsNextJS                bool
	DetectionStatus         string
	ScanStatus              string
	BuildID                 string
	AssetPrefix             string
	ObservedAssetPrefixes   []string
	Router                  string
	Bundler                 string
	Adapter                 string
	Platform                string
	DeploymentID            string
	Routes                  map[string][]string
	DiscoveredURLs          []string
	AllAssets               map[string]bool
	ManifestFound           bool
	ManifestExecOK          bool
	ManifestStatus          string
	ExecutionError          error `json:"-"`
	NextDataJSONRaw         string
	DetectedNextVersion     string
	DetectedReactVersion    string
	HTTPStatus              int
	ResponseHeaders         http.Header
	Redirects               []fetch.Redirect
	TLSVerificationDisabled bool
	Findings                []versiondetect.Finding
	Warnings                []string
}
type Options struct {
	ProbeRSC       bool
	DiscoverRoutes bool
	CrawlPages     int
}
type Scanner struct {
	fetcher         fetch.Fetcher
	versionDetector versiondetect.VersionDetector
	customBaseURL   string
	Options         Options
}

func NewScanner(f fetch.Fetcher, d versiondetect.VersionDetector, custom string) *Scanner {
	return &Scanner{fetcher: f, versionDetector: d, customBaseURL: custom}
}
func (s *Scanner) ScanTarget(target string) (*ScanResult, error) {
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}
	r := &ScanResult{BaseURL: target, DetectionStatus: "unknown", ScanStatus: "failed", Router: "Unknown", Bundler: "Unknown", Adapter: "Unknown", Platform: "Unknown", DetectedNextVersion: "Unknown", DetectedReactVersion: "Unknown", ManifestStatus: "not_applicable", Routes: map[string][]string{}, AllAssets: map[string]bool{}}
	started := time.Now()
	log.Printf("Scan: fetching initial page %s", target)
	defer func() {
		log.Printf("Scan: %s in %s; detection=%s, Next.js=%s, React=%s, assets=%d, warnings=%d", r.ScanStatus, time.Since(started).Round(time.Millisecond), r.DetectionStatus, r.DetectedNextVersion, r.DetectedReactVersion, len(r.AllAssets), len(r.Warnings))
		if r.ExecutionError != nil {
			log.Printf("Scan: failed: %v", r.ExecutionError)
		}
	}()
	res, err := fetch.Read(s.fetcher, fetch.Request{URL: target})
	if res != nil {
		r.BaseURL = res.URL
		r.HTTPStatus = res.StatusCode
		r.ResponseHeaders = res.Headers
		r.Redirects = res.Redirects
		r.TLSVerificationDisabled = res.TLSVerificationDisabled
		log.Printf("Initial page: HTTP %d, %d bytes, final URL %s", res.StatusCode, len(res.Body), res.URL)
		r.inspectHeaders(res)
	}
	if err != nil {
		r.ExecutionError = err
		r.resolve()
		return r, err
	}
	base, err := url.Parse(res.URL)
	if err != nil {
		r.ExecutionError = err
		return r, err
	}
	r.ScanStatus = "complete"
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		r.ScanStatus = "partial"
		r.warn(fmt.Sprintf("initial response HTTP %d", res.StatusCode))
	}
	assetBase := *base
	assetBase.RawQuery = ""
	assetBase.Fragment = ""
	assetBase.Path = ""
	assetBase.RawPath = ""
	data, raw, dataErr := findAndParseNextData(strings.NewReader(string(res.Body)))
	r.NextDataJSONRaw = raw
	if errors.Is(dataErr, ErrNextDataMissing) {
		log.Println("Next data: __NEXT_DATA__ absent; continuing with independent App Router and asset techniques")
	} else if dataErr == nil {
		log.Printf("Next data: parsed __NEXT_DATA__, build ID %s", data.BuildID)
	}
	if dataErr != nil && !errors.Is(dataErr, ErrNextDataMissing) {
		r.warn(dataErr.Error())
	}
	if data != nil {
		r.add("framework", "nextjs", "next-data", "high", res.URL, "__NEXT_DATA__")
		r.add("router", "pages", "next-data", "high", res.URL, "__NEXT_DATA__")
		r.AssetPrefix = data.AssetPrefix
		if data.BuildID != "" {
			r.add("build-id", data.BuildID, "next-data", "high", res.URL, "buildId")
		}
		if data.AssetPrefix != "" {
			prefix, e := base.Parse(data.AssetPrefix)
			if e == nil {
				assetBase = *prefix
			}
		}
	}
	customAssets := false
	if s.customBaseURL != "" {
		custom, e := url.Parse(s.customBaseURL)
		if e != nil || custom.Host == "" || (custom.Scheme != "http" && custom.Scheme != "https") {
			r.warn("invalid asset base override; using detected asset base")
		} else {
			assetBase = *custom
			customAssets = true
			// Preserve a declared prefix when overriding the serving origin.
			if r.AssetPrefix != "" {
				if prefix, e := url.Parse(r.AssetPrefix); e == nil && prefix.Path != "" && !strings.HasSuffix(assetBase.Path, prefix.Path) {
					assetBase.Path = path.Join(assetBase.Path, prefix.Path)
				}
			}
			assetBase.RawQuery = ""
			assetBase.Fragment = ""
			assetBase.RawPath = ""
		}
	}
	r.AssetBaseURL = strings.TrimRight(assetBase.String(), "/")
	log.Printf("HTML: inspecting assets and inline Flight; asset base %s", r.AssetBaseURL)
	r.beginObservation(target, res, "html")
	r.inspectHTML(string(res.Body), base, &assetBase, customAssets)
	r.endObservation()
	log.Printf("HTML: discovered %d assets and %d same-origin URLs", len(r.AllAssets), len(r.DiscoveredURLs))
	if strings.Contains(res.Headers.Get("Content-Type"), "text/x-component") {
		r.beginObservation(target, res, "rsc")
		r.inspectFlight(string(res.Body), base, &assetBase, customAssets, "rsc")
		r.endObservation()
	}
	if s.Options.ProbeRSC {
		log.Printf("RSC: probing %s with RSC: 1", base.String())
		probeURL := *base
		q := probeURL.Query()
		q.Set("_rsc", "nextr4y")
		probeURL.RawQuery = q.Encode()
		probe, e := fetch.Read(s.fetcher, fetch.Request{URL: probeURL.String(), Headers: http.Header{"Rsc": []string{"1"}}})
		if e != nil {
			r.warn("RSC probe: " + e.Error())
		} else if strings.Contains(probe.Headers.Get("Content-Type"), "text/x-component") {
			log.Printf("RSC: received HTTP %d, %d bytes of Flight data", probe.StatusCode, len(probe.Body))
			r.inspectHeaders(probe)
			u, _ := url.Parse(probe.URL)
			r.beginObservation(base.String(), probe, "rsc")
			r.inspectFlight(string(probe.Body), u, &assetBase, customAssets, "rsc")
			r.endObservation()
		} else {
			r.warn(fmt.Sprintf("RSC probe returned HTTP %d %s", probe.StatusCode, probe.Headers.Get("Content-Type")))
		}
	}
	r.resolve()
	if r.AssetPrefix != "" {
		if prefix, e := base.Parse(r.AssetPrefix); e == nil {
			if !customAssets {
				assetBase = *prefix
			} else if prefix.Path != "" && !strings.HasSuffix(assetBase.Path, prefix.Path) {
				assetBase.Path = path.Join(assetBase.Path, prefix.Path)
			}
			r.AssetBaseURL = strings.TrimRight(assetBase.String(), "/")
		}
	}
	// Keep the current page's asset scope for fingerprinting. Enumerating a
	// manifest must not trigger hundreds of downloads of unrelated pages.
	pageAssets := map[string]bool{}
	for u := range r.AllAssets {
		pageAssets[u] = true
	}
	s.probeManifests(r, base)
	log.Printf("Manifest: %s; %d routes mapped", r.ManifestStatus, len(r.Routes))
	js := map[string]bool{}
	for _, assets := range []map[string]bool{pageAssets, r.ManifestAssets} {
		for u := range assets {
			parsed, e := url.Parse(u)
			if e == nil && strings.HasSuffix(parsed.Path, ".js") {
				js[u] = true
			}
		}
		if len(js) > 0 {
			break
		}
	}
	log.Printf("Versions: inspecting %d JavaScript assets", len(js))
	if detailed, ok := s.versionDetector.(versiondetect.DetailedDetector); ok {
		report := detailed.DetectEvidence(js, s.fetcher)
		r.Findings = append(r.Findings, report.Findings...)
		r.ChunkMaps = append(r.ChunkMaps, report.ChunkMaps...)
		for _, warning := range report.Warnings {
			r.warn(warning)
		}
	} else if s.versionDetector != nil {
		n, react := s.versionDetector.Detect(r.BuildID, js, &assetBase, s.fetcher)
		for prop, value := range map[string]string{"next-version": n, "react-version": react} {
			if value != "" && value != "Unknown" {
				r.add(prop, value, "legacy-detector", "medium", res.URL, "legacy detector result")
			}
		}
	}
	if s.Options.DiscoverRoutes {
		log.Printf("Sitemap: probing %s/sitemap.xml", base.Scheme+"://"+base.Host)
		s.discover(r, base)
		log.Printf("Sitemap: discovery finished; %d total URLs", len(r.DiscoveredURLs))
	}
	if s.Options.CrawlPages > 0 {
		s.crawl(r, base)
	}
	r.resolve()
	if len(r.Warnings) > 0 {
		r.ScanStatus = "partial"
	}
	if !r.IsNextJS && res.StatusCode >= 200 && res.StatusCode < 300 && r.ScanStatus == "complete" {
		r.DetectionStatus = "not_detected"
	}
	sort.Strings(r.Warnings)
	sort.Strings(r.DiscoveredURLs)
	sort.Strings(r.ObservedAssetPrefixes)
	return r, nil
}
func (r *ScanResult) add(property, value, technique, confidence, u, evidence string) {
	for _, f := range r.Findings {
		if f.Property == property && f.Value == value && f.Technique == technique && f.URL == u {
			return
		}
	}
	if technique != "assets" {
		log.Printf("Detection (%s): %s=%s [%s]", technique, property, value, confidence)
	}
	r.Findings = append(r.Findings, versiondetect.Finding{Property: property, Value: value, Technique: technique, Confidence: confidence, URL: u, Evidence: evidence})
}
func (r *ScanResult) resolve() {
	r.ServingPlatforms = nil
	for _, f := range r.Findings {
		if f.Property == "platform" && !contains(r.ServingPlatforms, f.Value) {
			r.ServingPlatforms = append(r.ServingPlatforms, f.Value)
		}
	}
	sort.Strings(r.ServingPlatforms)
	r.Platform = "Unknown"
	if len(r.ServingPlatforms) > 0 {
		r.Platform = strings.Join(r.ServingPlatforms, ", ")
	}

	for property, dst := range map[string]*string{"next-version": &r.DetectedNextVersion, "react-version": &r.DetectedReactVersion, "router": &r.Router, "bundler": &r.Bundler, "adapter": &r.Adapter, "build-id": &r.BuildID, "deployment-id": &r.DeploymentID} {
		value, conflict := versiondetect.Resolve(r.Findings, property)
		if conflict && property == "router" {
			value = "mixed"
			conflict = false
		}
		if conflict {
			warning := "conflicting " + property + " evidence"
			if !contains(r.Warnings, warning) {
				r.warn(warning)
			}
		}
		if value == "Unknown" && (property == "build-id" || property == "deployment-id") {
			value = ""
		}
		*dst = value
	}
	for _, f := range r.Findings {
		if f.Property == "framework" && f.Value == "nextjs" || f.Property == "next-version" && f.Confidence == "high" {
			r.IsNextJS = true
			r.DetectionStatus = "detected"
		}
	}
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (r *ScanResult) inspectHeaders(res *fetch.Response) {
	h := res.Headers
	if strings.Contains(strings.ToLower(h.Get("X-Powered-By")), "next.js") {
		r.add("framework", "nextjs", "headers", "high", res.URL, "x-powered-by: "+h.Get("X-Powered-By"))
	}
	if h.Get("X-Opennext") != "" || h.Get("X-Opennext-Cache") != "" {
		r.add("adapter", "opennext", "headers", "high", res.URL, "x-opennext: "+h.Get("X-Opennext")+"; x-opennext-cache: "+h.Get("X-Opennext-Cache"))
	}
	if strings.EqualFold(h.Get("Server"), "cloudflare") || h.Get("Cf-Ray") != "" {
		r.add("platform", "cloudflare", "headers", "high", res.URL, "Cloudflare serving infrastructure")
	}
	if strings.EqualFold(h.Get("Server"), "vercel") || h.Get("X-Vercel-Id") != "" {
		r.add("platform", "vercel", "headers", "high", res.URL, "Vercel serving infrastructure")
	}
}

func (r *ScanResult) warn(message string) {
	log.Printf("Warning: %s", message)
	r.Warnings = append(r.Warnings, message)
}

func (r *ScanResult) beginObservation(requested string, res *fetch.Response, technique string) {
	r.observation = &PageObservation{RequestedURL: requested, FinalURL: res.URL, Technique: technique, HTTPStatus: res.StatusCode, Assets: []string{}}
}
func (r *ScanResult) endObservation() {
	if r.observation == nil {
		return
	}
	sort.Strings(r.observation.Assets)
	var scoped []versiondetect.Finding
	for _, f := range r.Findings {
		if f.URL == r.observation.FinalURL {
			scoped = append(scoped, f)
		}
	}
	build, _ := versiondetect.Resolve(scoped, "build-id")
	if build != "Unknown" {
		r.observation.BuildID = build
	}
	r.observation.AssetPrefix = r.AssetPrefix
	r.PageObservations = append(r.PageObservations, *r.observation)
	r.observation = nil
}
