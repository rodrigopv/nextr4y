package versiondetect

import (
	"net/url"

	"github.com/rodrigopv/nextr4y/internal/fetch"
)

// VersionDetector defines the interface for strategies that detect Next.js and React versions.
type VersionDetector interface {
	// Detect attempts to find the Next.js and React versions using a specific strategy.
	// It takes the build ID (if known), a map of all JS asset URLs (from HTML and manifest),
	// the parsed base URL for assets, and a fetcher to retrieve content.
	// It returns the detected Next.js version and React version strings.
	// "Unknown" or a fallback value (like ">=13...") should be returned if detection fails.
	Detect(buildID string, jsAssetURLs map[string]bool, assetBaseURL *url.URL, fetcher fetch.Fetcher) (nextVersion string, reactVersion string)
} 