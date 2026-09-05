package scanner

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/rodrigopv/nextr4y/internal/fetch"
)

var flightPush = regexp.MustCompile(`self\.__next_f\.push\(\s*`)

func (r *ScanResult) asset(raw string, base, override *url.URL, custom bool) {
	u, e := base.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.Contains(u.Path, "/_next/static/") {
		return
	}
	if !strings.HasSuffix(u.Path, ".js") && !strings.HasSuffix(u.Path, ".css") {
		return
	}
	at := strings.Index(u.Path, "/_next/")
	prefix := u.Path[:at]
	if u.Host != base.Host || u.Scheme != base.Scheme {
		prefix = u.Scheme + "://" + u.Host + prefix
	}
	if !contains(r.ObservedAssetPrefixes, prefix) {
		r.ObservedAssetPrefixes = append(r.ObservedAssetPrefixes, prefix)
	}
	r.add("framework", "nextjs", "assets", "medium", base.String(), "Next.js static asset: "+u.String())
	if id := u.Query().Get("dpl"); id != "" {
		r.add("deployment-id", id, "asset-query", "high", u.String(), "dpl query parameter")
	}
	if custom {
		tail := u.Path[at:]
		root := strings.TrimRight(override.Path, "/")
		if strings.HasSuffix(root, "/_next") {
			tail = strings.TrimPrefix(tail, "/_next")
		}
		u.Scheme = override.Scheme
		u.Host = override.Host
		u.Path = root + tail
		u.RawPath = ""
	}
	r.AllAssets[u.String()] = true
	if r.observation != nil && !contains(r.observation.Assets, u.String()) {
		r.observation.Assets = append(r.observation.Assets, u.String())
	}
}
func (r *ScanResult) inspectHTML(body string, base, override *url.URL, custom bool) {
	doc, e := goquery.NewDocumentFromReader(strings.NewReader(body))
	if e != nil {
		r.warn(e.Error())
		return
	}
	doc.Find("script[src], link[rel=preload], link[rel=modulepreload], link[rel=stylesheet]").Each(func(_ int, s *goquery.Selection) {
		raw, ok := s.Attr("src")
		if !ok {
			raw, _ = s.Attr("href")
		}
		r.asset(raw, base, override, custom)
	})
	var flight strings.Builder
	doc.Find("script:not([src])").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		for _, index := range flightPush.FindAllStringIndex(text, -1) {
			var values []json.RawMessage
			dec := json.NewDecoder(strings.NewReader(text[index[1]:]))
			if e := dec.Decode(&values); e != nil || len(values) < 2 {
				continue
			}
			var kind int
			var part string
			if json.Unmarshal(values[0], &kind) == nil && kind == 1 && json.Unmarshal(values[1], &part) == nil {
				flight.WriteString(part)
			}
		}
	})
	if flight.Len() > 0 {
		r.inspectFlight(flight.String(), base, override, custom, "inline-flight")
	}
	// Links are observed URLs, not a claim to have enumerated every server route.
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) { href, _ := s.Attr("href"); r.discoverURL(base, href) })
}
func (r *ScanResult) inspectFlight(body string, base, override *url.URL, custom bool, technique string) {
	valid := false
	prefix := r.AssetPrefix
	// Import records can precede record 0. Read metadata first so the prefix is
	// known before resolving any relative chunk filename.
	for _, line := range strings.Split(body, "\n") {
		id, payload, ok := strings.Cut(line, ":")
		if !ok || id != "0" {
			continue
		}
		var root map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &root) != nil {
			var legacy []interface{}
			if json.Unmarshal([]byte(payload), &legacy) == nil && r.inspectLegacyFlight(legacy, base, technique, 0) {
				valid = true
				prefix = r.AssetPrefix
			}
			continue
		}
		if root["f"] == nil && root["c"] == nil {
			continue
		}
		valid = true
		var build string
		if json.Unmarshal(root["b"], &build) == nil && build != "" {
			r.add("build-id", build, technique, "high", base.String(), "Flight root b")
		}
		// Older payloads use p for assetPrefix; newer payloads may put a Flight
		// reference here. Accept a literal URL/path only, never "$undefined" etc.
		var declared string
		if json.Unmarshal(root["p"], &declared) == nil && validAssetPrefix(declared) {
			prefix = declared
			r.AssetPrefix = declared
			r.add("asset-prefix", declared, technique, "high", base.String(), "Flight root p")
		}
	}
	flightBase := *override
	if !custom {
		if prefix != "" {
			if u, e := base.Parse(prefix); e == nil {
				flightBase = *u
			}
		}
		// A single observed asset root can resolve legacy payloads without p.
		if prefix == "" && len(r.ObservedAssetPrefixes) == 1 {
			if u, e := base.Parse(r.ObservedAssetPrefixes[0]); e == nil {
				flightBase = *u
			}
		}
	} else if prefix != "" {
		if u, e := url.Parse(prefix); e == nil && u.Path != "" && !strings.HasSuffix(flightBase.Path, u.Path) {
			flightBase.Path = path.Join(flightBase.Path, u.Path)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		_, payload, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(payload, "I[") {
			continue
		}
		var imported []interface{}
		if json.Unmarshal([]byte(payload[1:]), &imported) != nil || len(imported) < 2 {
			continue
		}
		chunks, ok := imported[1].([]interface{})
		if !ok {
			continue
		}
		for _, chunk := range chunks {
			raw, ok := chunk.(string)
			if !ok {
				continue
			}
			// Explicit /_next or absolute CDN paths already carry their own location.
			if strings.HasPrefix(raw, "chunks/") {
				raw = "static/" + raw
			}
			if strings.HasPrefix(raw, "static/") {
				root := *(&flightBase)
				root.RawQuery = ""
				root.Fragment = ""
				root.RawPath = ""
				if !strings.HasSuffix(strings.TrimRight(root.Path, "/"), "/_next") {
					root.Path = path.Join(root.Path, "_next")
				}
				root.Path = strings.TrimRight(root.Path, "/") + "/"
				ref, e := url.Parse(raw)
				if e != nil {
					continue
				}
				raw = root.ResolveReference(ref).String()
				// The explicit override has already been applied once above.
				r.asset(raw, base, &flightBase, false)
			} else {
				r.asset(raw, base, &flightBase, custom)
			}
		}
	}
	if valid {
		r.add("framework", "nextjs", technique, "high", base.String(), "Next.js Flight root")
		r.add("router", "app", technique, "high", base.String(), "Next.js Flight root")
	}
}
func validAssetPrefix(prefix string) bool {
	if strings.HasPrefix(prefix, "$") {
		return false
	}
	u, e := url.Parse(prefix)
	return e == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "" || u.Scheme == "http" || u.Scheme == "https")
}
func (r *ScanResult) discoverURL(base *url.URL, raw string) {
	if len(r.DiscoveredURLs) >= 256 {
		return
	}
	u, e := base.Parse(raw)
	if e != nil || u.Host != base.Host || u.Scheme != base.Scheme {
		return
	}
	u.Fragment = ""
	if !contains(r.DiscoveredURLs, u.String()) {
		r.DiscoveredURLs = append(r.DiscoveredURLs, u.String())
	}
}
func (s *Scanner) discover(r *ScanResult, base *url.URL) {
	u := *base
	u.Path = "/sitemap.xml"
	u.RawQuery = ""
	u.Fragment = ""
	res, e := fetch.Read(s.fetcher, fetch.Request{URL: u.String()})
	if e != nil {
		r.warn("sitemap: " + e.Error())
		return
	}
	if res.StatusCode != 200 {
		return
	}
	var sitemap struct {
		XMLName xml.Name
		URLs    []struct {
			Location string `xml:"loc"`
		} `xml:"url"`
	}
	if e := xml.Unmarshal(res.Body, &sitemap); e != nil {
		r.warn("sitemap: invalid XML")
		return
	}
	if sitemap.XMLName.Local != "urlset" {
		r.warn("sitemap index traversal is not supported")
		return
	}
	for _, item := range sitemap.URLs {
		r.discoverURL(base, item.Location)
	}
	if len(r.DiscoveredURLs) >= 256 {
		r.warn(fmt.Sprintf("discovered URL limit reached (%d)", 256))
	}
}

// Only accept legacy router props with both initialTree and initialSeedData.
// A random buildId buried in application props is not framework metadata.
func (r *ScanResult) inspectLegacyFlight(value interface{}, base *url.URL, technique string, depth int) bool {
	if depth > 64 {
		return false
	}
	tuple, ok := value.([]interface{})
	if !ok {
		return false
	}
	found := false
	marker := ""
	if len(tuple) > 0 {
		marker, _ = tuple[0].(string)
	}
	if len(tuple) == 4 && marker == "$" {
		if props, ok := tuple[3].(map[string]interface{}); ok && props["initialTree"] != nil && props["initialSeedData"] != nil {
			if build, ok := props["buildId"].(string); ok && build != "" {
				r.add("build-id", build, technique, "high", base.String(), "legacy Flight router buildId")
				if prefix, ok := props["assetPrefix"].(string); ok && validAssetPrefix(prefix) {
					r.AssetPrefix = prefix
					r.add("asset-prefix", prefix, technique, "high", base.String(), "legacy Flight assetPrefix")
				}
				found = true
			}
		}
	}
	for _, child := range tuple {
		if r.inspectLegacyFlight(child, base, technique, depth+1) {
			found = true
		}
	}
	return found
}
