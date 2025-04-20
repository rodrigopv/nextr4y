package versiondetect

import (
	"bytes"
	"io"
	"log"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/rodrigopv/nextr4y/internal/fetch"
)

// Regexes for version detection
var simpleVersionRegex = regexp.MustCompile(`["'](\d+\.\d+\.\d+[^"']*)["']`)
var windowNextDirectVersionRegex = regexp.MustCompile(`window\.next\s*=\s*{[\\s\\S]*?version\s*:\s*[\"\'](\\d+\\.\\d+\\.\\d+[^\\\"\']*)[\"\']`)
var windowNextVarVersionRegex = regexp.MustCompile(`window\.next\s*=\s*{[\\s\\S]*?version\s*:\s*([a-zA-Z0-9_$]+(?:\\.[a-zA-Z0-9_$]+)?)`)
var assignmentVersionRegex = regexp.MustCompile(`(?:let|var|const)\s+[a-zA-Z0-9_$]+\s*=\s*[\"\'](\\d+\\.\\d+\\.\\d+[^\"\']*)[\"\']`)
var reactVersionInContextRegex = regexp.MustCompile(`version\\s*:\\s*[\"\'](\\d+\\.\\d+\\.\\d+[^\"\']*)[\"\']`)

// HeuristicAssetScannerDetector implements VersionDetector using regex scanning of JS assets.
// It prioritizes core chunks and uses context checks to differentiate Next.js and React.
type HeuristicAssetScannerDetector struct{}

var _ VersionDetector = (*HeuristicAssetScannerDetector)(nil)

type fetchFunc func(assetURL string, stage string) ([]byte, bool)

// detectWithWindowNextPattern searches URLs for the specific window.next.version pattern (direct or via variable).
func detectWithWindowNextPattern(urls []string, fetchContent fetchFunc, stagePrefix string) (version string, found bool) {
	log.Printf("Version check (%s): Searching %d URLs for window.next patterns...", stagePrefix, len(urls))
	for _, assetURL := range urls {
		contentBytes, ok := fetchContent(assetURL, stagePrefix+" window.next patterns")
		if !ok { continue }

		// Try direct assignment regex
		matchDirect := windowNextDirectVersionRegex.FindSubmatch(contentBytes)
		if len(matchDirect) > 1 {
			foundVersion := string(matchDirect[1])
			log.Printf("Version check (%s): Found specific Next.js version '%s' (via direct window.next regex) in %s", stagePrefix, foundVersion, assetURL)
			return foundVersion, true
		}

		// Try variable assignment regex
		matchVar := windowNextVarVersionRegex.FindSubmatchIndex(contentBytes)
		if matchVar != nil {
			// Found window.next = { version: someVariable }
			// Search the entire file for the first likely version assignment
			varIdentifierBytes := contentBytes[matchVar[2]:matchVar[3]]
			varIdentifier := string(varIdentifierBytes)
			log.Printf("Version check (%s): Found window.next assignment via variable '%s' in %s. Searching *entire file* for version assignment...", stagePrefix, varIdentifier, assetURL)
			
			// Look for patterns like let H = "15.2.0" anywhere in this file
			assignmentMatch := assignmentVersionRegex.FindSubmatch(contentBytes)
			if len(assignmentMatch) > 1 {
				foundVersion := string(assignmentMatch[1])
				log.Printf("Version check (%s): Found potential version '%s' (via file-wide assignment regex) after finding variable use in %s", stagePrefix, foundVersion, assetURL)
				return foundVersion, true
			}

			// Fallback: Use simple regex across the whole file
			simpleMatch := simpleVersionRegex.FindSubmatch(contentBytes)
			if len(simpleMatch) > 1 {
				foundVersion := string(simpleMatch[1])
				log.Printf("Version check (%s): Found potential version '%s' (via file-wide simple regex fallback) after finding variable use in %s", stagePrefix, foundVersion, assetURL)
				return foundVersion, true
			}
			log.Printf("Version check (%s): Could not find any version assignment in file %s despite finding variable use.", stagePrefix, assetURL)
		}
	}
	log.Printf("Version check (%s): window.next patterns did not yield version in provided URLs.", stagePrefix)
	return "", false
}

// detectWithSimpleContextPattern searches URLs using simple regex and context analysis.
func detectWithSimpleContextPattern(urls []string, fetchContent fetchFunc, currentNextVersion, currentReactVersion string) (foundNext string, foundReact string) {
	log.Printf("Version check (Simple Context): Searching %d URLs with simple regex + context...", len(urls))
	nextVersion := currentNextVersion
	reactVersion := currentReactVersion

	for _, assetURL := range urls {
		if nextVersion != "" && reactVersion != "" { break }

		contentBytes, ok := fetchContent(assetURL, "Simple Context Scan")
		if !ok { continue }

		matches := simpleVersionRegex.FindAllSubmatch(contentBytes, -1)
		for _, match := range matches {
			if len(match) < 2 { continue }
			candidateVersion := string(match[1])
			fullMatchText := string(match[0])

			if nextVersion != "" && reactVersion != "" { break }

			matchIndex := bytes.Index(contentBytes, match[0])
			contextWindow := 30
			// Using built-in min/max functions from Go 1.21+
			start := max(0, matchIndex-contextWindow)
			end := min(len(contentBytes), matchIndex+len(match[0])+contextWindow)
			context := string(contentBytes[start:end])
			contextCleaned := strings.ReplaceAll(context, "\\n", " ")

			isReact := strings.Contains(context, "react") || strings.Contains(context, "React") || strings.Contains(context, "react-dom")
			isReconciler := strings.Contains(context, "reconcilerVersion")

			if isReact && reactVersion == "" {
				reactVersion = candidateVersion
				log.Printf("Version check (Simple Context): Found potential React version '%s' (Full Match: '%s', Context: '%s') in %s",
					candidateVersion, fullMatchText, contextCleaned, assetURL)
			} else if !isReact && !isReconciler && nextVersion == "" {
				nextVersion = candidateVersion
				log.Printf("Version check (Simple Context): Found potential Next.js version '%s' (Full Match: '%s', Context: '%s') in %s",
					candidateVersion, fullMatchText, contextCleaned, assetURL)
			}

			if nextVersion != "" && reactVersion != "" { break }
		}
	}
	log.Println("Version check (Simple Context): Scan complete.")
	return nextVersion, reactVersion
}

// detectWithAppManifestProbe checks for the existence of _appManifest.js.
func detectWithAppManifestProbe(buildID string, assetBaseURL *url.URL, fetcher fetch.Fetcher) (versionHint string, found bool) {
	if buildID == "" || assetBaseURL == nil || fetcher == nil {
		log.Println("Version check (App Manifest Probe): Skipping due to missing buildID, assetBaseURL, or fetcher.")
		return "Unknown (Missing data)", false
	}

	log.Println("Version check (App Manifest Probe): Probing for App Router manifest (_appManifest.js)...")
	appManifestPath := path.Join("_next/static", buildID, "_appManifest.js")
	appManifestURLRel := &url.URL{Path: appManifestPath}
	fullAppManifestURL := assetBaseURL.ResolveReference(appManifestURLRel).String()

	log.Printf("Version check (App Manifest Probe): Probing URL: %s", fullAppManifestURL)
	reader, _, err := fetcher.Fetch(fullAppManifestURL)
	if err == nil {
		reader.Close()
		log.Println("Version check (App Manifest Probe): _appManifest.js found.")
		return ">=13 (App Router Likely)", true
	}

	errStr := err.Error()
	if strings.Contains(errStr, "bad status code") && (strings.Contains(errStr, ": 404") || strings.Contains(errStr, ": 403")) {
		log.Println("Version check (App Manifest Probe): _appManifest.js not found (404/403).")
		return "<13 / Pages Router Likely", true
	}

	log.Printf("Version check (App Manifest Probe): Error probing: %v", err)
	return "Unknown (Error probing)", false
}

// Detect attempts to fingerprint Next.js and React versions using asset scanning strategies.
func (d *HeuristicAssetScannerDetector) Detect(buildID string, jsAssetURLs map[string]bool, assetBaseURL *url.URL, fetcher fetch.Fetcher) (nextVersion string, reactVersion string) {
	if fetcher == nil {
		return "Unknown (Missing fetcher)", "Unknown (Missing fetcher)"
	}

	finalNextVersion := ""
	finalReactVersion := ""

	// Prepare URL Lists
	priorityURLs := []string{}
	otherURLs := []string{}
	for u := range jsAssetURLs {
		parsedURL, err := url.Parse(u)
		if err == nil && (strings.Contains(path.Base(parsedURL.Path), "framework") || strings.Contains(path.Base(parsedURL.Path), "main")) {
			priorityURLs = append(priorityURLs, u)
		} else {
			otherURLs = append(otherURLs, u)
		}
	}
	sort.Strings(priorityURLs)
	sort.Strings(otherURLs)
	allURLs := append(priorityURLs, otherURLs...)
	sort.Strings(allURLs)

	// Fetch Content Helper
	fetchContent := func(assetURL string, stage string) ([]byte, bool) {
		log.Printf("Version check (%s): Probing %s", stage, assetURL)
		reader, _, err := fetcher.Fetch(assetURL)
		if err != nil {
			log.Printf("Version check (%s): Failed to fetch asset %s: %v", stage, assetURL, err)
			return nil, false
		}
		defer reader.Close()
		contentBytes, readErr := io.ReadAll(reader)
		if readErr != nil {
			log.Printf("Version check (%s): Failed to read asset %s: %v", stage, assetURL, readErr)
			return nil, false
		}
		return contentBytes, true
	}

	// Strategy 1a: Try window.next pattern on priority URLs (for Next.js version)
	foundVersion, found := detectWithWindowNextPattern(priorityURLs, fetchContent, "Strategy 1a (Priority window.next)")
	if found {
		finalNextVersion = foundVersion
	}

	// Strategy 1b: Try simple context pattern on priority URLs (for React version)
	_, reactCand := detectWithSimpleContextPattern(priorityURLs, fetchContent, finalNextVersion, "")
	if reactCand != "" {
		finalReactVersion = reactCand
		log.Printf("Version check (Strategy 1b Priority React Context): Set React version to '%s' based on priority scan.", finalReactVersion)
	}

	// Strategy 1c: If Next.js not found yet, try window.next pattern on other URLs
	if finalNextVersion == "" {
		foundVersion, found = detectWithWindowNextPattern(otherURLs, fetchContent, "Strategy 1c (Other window.next)")
		if found {
			finalNextVersion = foundVersion
		}
	}

	// Strategy 2: Try simple regex with context on ALL URLs (Fallback for anything not found yet)
	if finalNextVersion == "" || finalReactVersion == "" {
		log.Printf("Version check (Strategy 2 Fallback Context): Running simple context scan on ALL URLs for missing versions (Next?: %t, React?: %t).", finalNextVersion == "", finalReactVersion == "")
		nextCandFallback, reactCandFallback := detectWithSimpleContextPattern(allURLs, fetchContent, finalNextVersion, finalReactVersion)
		if finalNextVersion == "" && nextCandFallback != "" {
			finalNextVersion = nextCandFallback
		}
		if finalReactVersion == "" && reactCandFallback != "" {
			finalReactVersion = reactCandFallback
		}
	}

	// Strategy 3: Fallback - App Manifest Probe (only if Next version still unknown)
	if finalNextVersion == "" {
		versionHint, foundHint := detectWithAppManifestProbe(buildID, assetBaseURL, fetcher)
		if foundHint {
			finalNextVersion = versionHint
		}
	}

	// Final Cleanup
	if finalNextVersion == "" {
		log.Println("Version check: Could not determine Next.js version through any strategy.")
		finalNextVersion = "Unknown"
	} else {
		log.Printf("Version check: Final determined Next.js version/hint: %s", finalNextVersion)
	}

	if finalReactVersion == "" {
		log.Println("Version check: Could not determine React version.")
		finalReactVersion = "Unknown"
	} else {
		log.Printf("Version check: Final determined React version: %s", finalReactVersion)
	}

	return finalNextVersion, finalReactVersion
}

