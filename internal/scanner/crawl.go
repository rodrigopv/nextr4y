package scanner

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/rodrigopv/nextr4y/internal/fetch"
)

// Follow only discovered same-origin URLs, not route templates or Webpack IDs.
// Each page keeps its own metadata so another app/build at the same host does
// not overwrite the target's framework version, build ID, or asset prefix.
func (s *Scanner) crawl(r *ScanResult, base *url.URL) {
	limit := min(s.Options.CrawlPages, 32)
	key := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		if u.Path == "" {
			u.Path = "/"
		}
		u.Fragment = ""
		return u.String()
	}
	seen := map[string]bool{key(base.String()): true}
	visited := 0
	for i := 0; i < len(r.DiscoveredURLs) && visited < limit; i++ {
		target := r.DiscoveredURLs[i]
		if seen[key(target)] {
			continue
		}
		seen[key(target)] = true
		visited++
		log.Printf("Pages [%d/%d]: fetching %s", visited, limit, target)
		res, e := fetch.Read(s.fetcher, fetch.Request{URL: target})
		if e != nil {
			r.warn(fmt.Sprintf("page %s: %v", target, e))
			continue
		}
		final, e := url.Parse(res.URL)
		if e != nil || final.Host != base.Host || final.Scheme != base.Scheme {
			log.Printf("Pages: skipping redirected cross-origin response for %s", target)
			continue
		}
		seen[key(final.String())] = true
		if !strings.Contains(strings.ToLower(res.Headers.Get("Content-Type")), "text/html") {
			log.Printf("Pages: skipping non-HTML response for %s", target)
			continue
		}
		page := &ScanResult{AllAssets: map[string]bool{}}
		assetBase := *final
		assetBase.Path = ""
		assetBase.RawPath = ""
		assetBase.RawQuery = ""
		assetBase.Fragment = ""
		// An explicit override is user-supplied, never inferred from another page.
		custom := false
		if s.customBaseURL != "" {
			if u, e := url.Parse(s.customBaseURL); e == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
				assetBase = *u
				custom = true
			}
		}
		page.beginObservation(target, res, "html")
		page.inspectHTML(string(res.Body), final, &assetBase, custom)
		page.endObservation()
		r.PageObservations = append(r.PageObservations, page.PageObservations...)
		for u := range page.AllAssets {
			r.AllAssets[u] = true
		}
		for _, u := range page.DiscoveredURLs {
			r.discoverURL(base, u)
		}
		if res.StatusCode != 200 {
			r.warn(fmt.Sprintf("page %s: HTTP %d", target, res.StatusCode))
		}
		log.Printf("Pages: HTTP %d; associated %d assets with %s", res.StatusCode, len(page.AllAssets), res.URL)
	}
	log.Printf("Pages: finished %d additional page requests", visited)
}
