package versiondetect

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/rodrigopv/nextr4y/internal/chunkmap"
	"github.com/rodrigopv/nextr4y/internal/fetch"
)

// Finding is an attributed observation. Confidence is high, medium, or low.
// Unattributed semvers never participate in framework version resolution.
type Finding struct {
	Property   string
	Value      string
	Technique  string
	Confidence string
	URL        string
	Evidence   string
}
type Report struct {
	ChunkMaps []chunkmap.Inventory
	Findings  []Finding
	Warnings  []string
}
type DetailedDetector interface {
	DetectEvidence(map[string]bool, fetch.Fetcher) Report
}

var semver = regexp.MustCompile(`["'](\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)["']`)
var nextObject = regexp.MustCompile(`(?:window|self|globalThis)\.next\s*=\s*\{[^{}]{0,1000}\}`)
var versionProperty = regexp.MustCompile(`(?:\bversion|["']version["'])\s*:\s*["'](\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)["']`)
var rendererObject = regexp.MustCompile(`\{[^{}]{0,1000}\brendererPackageName\s*:\s*["']react-dom["'][^{}]{0,1000}\}`)
var appDir = regexp.MustCompile(`\bappDir\s*:\s*(?:!0|true)`)

// Each technique inspects its own input and emits findings without depending on
// another technique having succeeded. Adding a technique does not reorder winners.
type AssetTechnique struct {
	Name    string
	Inspect func(string, string) []Finding
}

func DefaultTechniques() []AssetTechnique {
	return []AssetTechnique{
		{"window.next", inspectNext},
		{"next-constant", inspectNextConstant},
		{"react-renderer", inspectReact},
		{"bundler", inspectBundler},
		{"semver-candidates", inspectCandidates},
	}
}

type HeuristicAssetScannerDetector struct {
	// Nil uses all built-in techniques. Callers may append independent techniques.
	Techniques []AssetTechnique
}

func finding(property, value, technique, u, evidence string) Finding {
	return Finding{Property: property, Value: value, Technique: technique, Confidence: "high", URL: u, Evidence: evidence}
}
func inspectNext(u, body string) []Finding {
	var out []Finding
	for _, object := range nextObject.FindAllString(body, -1) {
		if m := versionProperty.FindStringSubmatch(object); m != nil {
			out = append(out, finding("next-version", m[1], "window.next", u, object))
		}
		if appDir.MatchString(object) {
			out = append(out, finding("router", "app", "window.next", u, object))
		}
	}
	return out
}

// Only resolve an immediately preceding constant declaration. Searching an
// entire bundle for the first semver cannot establish a variable's binding.
var adjacentConstant = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(["'][^"']+["'])\s*;\s*((?:window|self|globalThis)\.next\s*=\s*\{[^{}]{0,1000}\})`)

func inspectNextConstant(u, body string) []Finding {
	var out []Finding
	for _, m := range adjacentConstant.FindAllStringSubmatch(body, -1) {
		v := semver.FindStringSubmatch(m[2])
		if v == nil {
			continue
		}
		use := regexp.MustCompile(`(?:\bversion|["']version["'])\s*:\s*` + regexp.QuoteMeta(m[1]) + `\s*[,}]`)
		if use.MatchString(m[3]) {
			out = append(out, finding("next-version", v[1], "next-constant", u, m[0]))
		}
	}
	return out
}
func inspectReact(u, body string) []Finding {
	var out []Finding
	// Match the actual renderer object, not the first occurrence of its version
	// elsewhere in a minified bundle (e.g. a compatibility assertion).
	for _, object := range rendererObject.FindAllString(body, -1) {
		if m := versionProperty.FindStringSubmatch(object); m != nil {
			out = append(out, finding("react-version", m[1], "react-renderer", u, object))
		}
	}
	return out
}

var turbopackRuntime = regexp.MustCompile(`(?:window\.next\.turbopack\s*=\s*(?:!0|true)|(?:globalThis|self)\.TURBOPACK\s*=)`)

func inspectBundler(u, body string) []Finding {
	if turbopackRuntime.MatchString(body) {
		return []Finding{finding("bundler", "turbopack", "bundler", u, "Turbopack runtime marker")}
	}
	if strings.Contains(body, "webpackChunk_N_E") {
		return []Finding{finding("bundler", "webpack", "bundler", u, "webpackChunk_N_E")}
	}
	return nil
}
func inspectCandidates(u, body string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, index := range semver.FindAllStringSubmatchIndex(body, 100) {
		value := body[index[2]:index[3]]
		// Keep the position of this occurrence, rather than bytes.Index(value).
		context := body[max(0, index[0]-60):min(len(body), index[1]+60)]
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, Finding{Property: "unattributed-version", Value: value, Technique: "semver-candidates", Confidence: "low", URL: u, Evidence: context})
	}
	return out
}
func (d *HeuristicAssetScannerDetector) DetectEvidence(assets map[string]bool, f fetch.Fetcher) Report {
	report := Report{}
	techniques := d.Techniques
	if techniques == nil {
		techniques = DefaultTechniques()
	}
	urls := make([]string, 0, len(assets))
	for u := range assets {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	// A per-call body snapshot is only transport reuse. Every technique still runs
	// independently on each successfully downloaded asset; failures aren't cached.
	if len(urls) > 128 {
		urls = urls[:128]
		report.Warnings = append(report.Warnings, "asset limit reached (128)")
	}
	for index, u := range urls {
		log.Printf("Asset [%d/%d]: fetching %s", index+1, len(urls), u)
		res, err := fetch.Read(f, fetch.Request{URL: u})
		if err != nil {
			log.Printf("Asset [%d/%d]: fetch failed: %v; continuing", index+1, len(urls), err)
			report.Warnings = append(report.Warnings, fmt.Sprintf("asset %s: %v", u, err))
			continue
		}
		log.Printf("Asset [%d/%d]: HTTP %d, %d bytes", index+1, len(urls), res.StatusCode, len(res.Body))
		if res.StatusCode != 200 {
			log.Println("Asset: skipping non-200 response")
			report.Warnings = append(report.Warnings, fmt.Sprintf("asset %s: HTTP %d", u, res.StatusCode))
			continue
		}
		body := string(res.Body)
		if strings.Contains(strings.ToLower(res.Headers.Get("Content-Type")), "text/html") || strings.HasPrefix(strings.TrimSpace(body), "<!DOCTYPE html") {
			log.Println("Asset: expected JavaScript but received HTML; skipping")
			report.Warnings = append(report.Warnings, fmt.Sprintf("asset %s returned HTML", u))
			continue
		}
		if inventory := chunkmap.Parse(res.URL, body); inventory != nil {
			report.ChunkMaps = append(report.ChunkMaps, *inventory)
			log.Printf("Webpack: recovered %d async chunk filenames (not routes)", len(inventory.Chunks))
		}
		for _, technique := range techniques {
			findings := technique.Inspect(u, body)
			report.Findings = append(report.Findings, findings...)
			if len(findings) == 0 {
				log.Printf("Technique %s: no match", technique.Name)
			}
			if technique.Name == "semver-candidates" && len(findings) > 0 {
				log.Printf("Technique %s: %d unattributed candidates (not accepted as framework versions)", technique.Name, len(findings))
			} else {
				for _, found := range findings {
					log.Printf("Technique %s: %s=%s [%s]", technique.Name, found.Property, found.Value, found.Confidence)
				}
			}
		}
	}
	return report
}

// Resolve refuses equally strong conflicting claims. The full evidence remains
// available to clients instead of allowing asset order to decide the version.
func Resolve(findings []Finding, property string) (string, bool) {
	rank := map[string]int{"high": 3, "medium": 2, "low": 1}
	best := 0
	values := map[string]bool{}
	for _, f := range findings {
		if f.Property != property || rank[f.Confidence] < 2 {
			continue
		}
		score := rank[f.Confidence]
		if score > best {
			best = score
			values = map[string]bool{}
		}
		if score == best {
			values[f.Value] = true
		}
	}
	if len(values) != 1 {
		return "Unknown", len(values) > 1
	}
	for value := range values {
		return value, false
	}
	return "Unknown", false
}
func (d *HeuristicAssetScannerDetector) Detect(_ string, assets map[string]bool, _ *url.URL, f fetch.Fetcher) (string, string) {
	r := d.DetectEvidence(assets, f)
	n, _ := Resolve(r.Findings, "next-version")
	react, _ := Resolve(r.Findings, "react-version")
	return n, react
}
