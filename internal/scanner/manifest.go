package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"github.com/rodrigopv/nextr4y/internal/fetch"
	"io"
	"log"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ErrNextDataMissing = errors.New("__NEXT_DATA__ script tag not found")
var manifestJSRegex = regexp.MustCompile(`self\.__BUILD_MANIFEST\s*=\s*(function\s*\(.*?\)\s*\{[\s\S]*?return\s*\{[\s\S]*?\}\s*\}\s*\(.*?\))`)

type NextData struct {
	BuildID     string                 `json:"buildId"`
	AssetPrefix string                 `json:"assetPrefix"`
	Props       map[string]interface{} `json:"props"`
}

// findAndParseNextData finds the __NEXT_DATA__ script and parses its JSON content.
func findAndParseNextData(htmlBody io.Reader) (*NextData, string, error) {
	doc, err := goquery.NewDocumentFromReader(htmlBody)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse HTML: %w", err)
	}

	jsonData := ""
	doc.Find("script#__NEXT_DATA__").Each(func(i int, s *goquery.Selection) {
		jsonData = s.Text()
	})

	if jsonData == "" {
		return nil, "", ErrNextDataMissing
	}

	var nextData NextData
	err = json.Unmarshal([]byte(jsonData), &nextData)
	if err != nil {
		return nil, jsonData, fmt.Errorf("failed to unmarshal __NEXT_DATA__ JSON: %w", err)
	}

	if nextData.BuildID == "" || nextData.Props == nil {
		return &nextData, jsonData, errors.New("__NEXT_DATA__ found, but missing expected fields (buildId, props)")
	}

	return &nextData, jsonData, nil
}

// executeManifestJS runs the manifest JS using goja.
func executeManifestJS(manifestJS string) (map[string]interface{}, error) {
	matches := manifestJSRegex.FindStringSubmatch(manifestJS)
	if len(matches) < 2 {
		log.Printf("Warning: Could not extract exact manifest expression via regex, attempting to run full script content.")
		if cbIndex := strings.Index(manifestJS, "self.__BUILD_MANIFEST_CB"); cbIndex != -1 {
			manifestJS = manifestJS[:cbIndex]
		}
		manifestJS = strings.TrimRight(manifestJS, "; ")
		if !strings.Contains(manifestJS, "=") {
			manifestJS = "(" + manifestJS + ")"
		} else {
			parts := strings.SplitN(manifestJS, "=", 2)
			if len(parts) == 2 {
				manifestJS = "(" + strings.TrimSpace(parts[1]) + ")"
			} else {
				return nil, errors.New("manifest JS structure not recognized for execution (fallback failed)")
			}
		}
	} else {
		manifestJS = "(" + matches[1] + ")"
	}

	vm := goja.New()
	timer := time.AfterFunc(time.Second, func() { vm.Interrupt("manifest execution timed out") })
	defer timer.Stop()
	_, err := vm.RunString("var self = {};")
	if err != nil {
		return nil, fmt.Errorf("goja: failed to define 'self': %w", err)
	}

	result, err := vm.RunString(manifestJS)
	if err != nil {
		return nil, fmt.Errorf("goja: failed to execute manifest JS: %w", err)
	}

	exported := result.Export()

	manifestMap, ok := exported.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("goja: manifest JS did not return an object, got type %T", exported)
	}

	return manifestMap, nil
}

// extractRoutesAndAssets processes the parsed manifest map.
func extractRoutesAndAssets(manifestData map[string]interface{}, assetBaseURL string) (map[string][]string, map[string]bool) {
	routes := make(map[string][]string)
	allAssets := make(map[string]bool)

	baseURLParsed, err := url.Parse(assetBaseURL)
	if err != nil {
		log.Printf("Warning: Could not parse asset base URL '%s': %v. Asset URLs might be incorrect.", assetBaseURL, err)
		baseURLParsed = &url.URL{}
	}

	for routePath, assetsInterface := range manifestData {
		if strings.HasPrefix(routePath, "__") || routePath == "sortedPages" {
			continue
		}

		assetList, ok := assetsInterface.([]interface{})
		if !ok {
			if assetStr, ok := assetsInterface.(string); ok {
				assetList = []interface{}{assetStr}
			} else {
				continue
			}
		}

		routeAssets := []string{}
		for _, assetPathInterface := range assetList {
			assetPath, ok := assetPathInterface.(string)
			if !ok {
				log.Printf("Warning: Skipping non-string asset in route '%s'", routePath)
				continue
			}

			ref, err := url.Parse(assetPath)
			if err != nil || (!strings.HasSuffix(ref.Path, ".js") && !strings.HasSuffix(ref.Path, ".css")) {
				continue
			}
			var resolvedURL *url.URL
			if ref.IsAbs() || ref.Host != "" {
				resolvedURL = baseURLParsed.ResolveReference(ref)
			} else {
				resolved := *baseURLParsed
				if strings.HasPrefix(ref.Path, "/_next/") {
					resolved.Path = path.Join(baseURLParsed.Path, ref.Path)
				} else {
					resolved.Path = path.Join(baseURLParsed.Path, "_next", ref.Path)
				}
				resolved.RawPath = ""
				resolved.RawQuery = ref.RawQuery
				resolved.Fragment = ref.Fragment
				resolvedURL = &resolved
			}
			if resolvedURL.Scheme != "http" && resolvedURL.Scheme != "https" {
				continue
			}
			fullAssetURL := resolvedURL.String()

			routeAssets = append(routeAssets, fullAssetURL)
			allAssets[fullAssetURL] = true
		}
		sort.Strings(routeAssets)
		routes[routePath] = routeAssets
	}

	return routes, allAssets
}

// Probe using any recovered build ID, independent of the current page's router.
// Multiple observed asset origins are candidates; absent manifests are normal.
func (s *Scanner) probeManifests(r *ScanResult, page *url.URL) {
	if r.BuildID == "" {
		log.Println("Manifest: skipped; no build ID recovered")
		return
	}
	roots := []string{}
	add := func(raw string) {
		u, e := page.Parse(raw)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		u.RawQuery = ""
		u.Fragment = ""
		u.RawPath = ""
		root := strings.TrimRight(u.String(), "/")
		if !strings.HasSuffix(root, "/_next") {
			root += "/_next"
		}
		if !contains(roots, root) && len(roots) < 4 {
			roots = append(roots, root)
		}
	}
	// Declared prefix/explicit override outrank inferred candidates.
	if r.AssetPrefix != "" || s.customBaseURL != "" {
		add(r.AssetBaseURL)
	} else {
		for _, prefix := range r.ObservedAssetPrefixes {
			add(prefix)
		}
		if len(roots) == 0 {
			add(r.AssetBaseURL)
		}
	}
	r.ManifestStatus = "unavailable"
	r.ManifestAssets = map[string]bool{}
	for _, root := range roots {
		target := root + "/static/" + url.PathEscape(r.BuildID) + "/_buildManifest.js"
		log.Printf("Manifest: fetching %s", target)
		observation := ManifestObservation{URL: target, Status: "unavailable"}
		response, e := fetch.Read(s.fetcher, fetch.Request{URL: target})
		if response != nil {
			observation.URL = response.URL
			observation.HTTPStatus = response.StatusCode
		}
		if e != nil {
			observation.Status = "failed"
			r.warn("build manifest: " + e.Error())
		} else if response.StatusCode == 200 && strings.Contains(string(response.Body), "__BUILD_MANIFEST") {
			r.ManifestFound = true
			parsed, e := executeManifestJS(string(response.Body))
			if e != nil {
				observation.Status = "invalid"
				r.warn("build manifest: " + e.Error())
			} else {
				observation.Status = "parsed"
				r.ManifestExecOK = true
				// Relative file entries are rooted at the manifest's actual /_next/ URL.
				manifestBase := root
				if i := strings.LastIndex(response.URL, "/_next/"); i >= 0 {
					manifestBase = response.URL[:i] + "/_next"
				}
				routes, assets := extractRoutesAndAssets(parsed, strings.TrimSuffix(manifestBase, "/_next"))
				observation.Routes = len(routes)
				for route, files := range routes {
					for _, file := range files {
						if !contains(r.Routes[route], file) {
							r.Routes[route] = append(r.Routes[route], file)
						}
					}
					sort.Strings(r.Routes[route])
					if r.RouteSources == nil {
						r.RouteSources = map[string][]string{}
					}
					if !contains(r.RouteSources[route], response.URL) {
						r.RouteSources[route] = append(r.RouteSources[route], response.URL)
					}
					if route != "/_app" && route != "/_error" && route != "/404" {
						for _, file := range files {
							if strings.Contains(file, "/chunks/pages/") {
								r.add("router", "pages", "build-manifest", "high", response.URL, "Pages route mappings")
								break
							}
						}
					}
				}
				for file := range assets {
					r.ManifestAssets[file] = true
					r.AllAssets[file] = true
				}
				log.Printf("Manifest: recovered %d route patterns and %d assets", len(routes), len(assets))
			}
		} else {
			log.Printf("Manifest: unavailable at %s (HTTP %d)", target, response.StatusCode)
			if response.StatusCode != 200 && response.StatusCode != 404 && response.StatusCode != 410 {
				r.warn(fmt.Sprintf("manifest %s: HTTP %d", target, response.StatusCode))
			}
		}
		r.Manifests = append(r.Manifests, observation)
	}
	if r.ManifestExecOK {
		r.ManifestStatus = "parsed"
	} else if r.ManifestFound {
		r.ManifestStatus = "invalid"
	}
}
