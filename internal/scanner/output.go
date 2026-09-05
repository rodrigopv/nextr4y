package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Preserve field names for existing JSON consumers, but represent unknown as
// null rather than false and serialize errors as useful strings rather than {}.
func (r ScanResult) MarshalJSON() ([]byte, error) {
	type alias ScanResult
	var detected *bool
	if r.DetectionStatus != "unknown" {
		b := r.IsNextJS
		detected = &b
	}
	var message *string
	if r.ExecutionError != nil {
		value := r.ExecutionError.Error()
		message = &value
	}
	return json.Marshal(struct {
		alias
		IsNextJS       *bool
		ExecutionError *string
	}{alias: alias(r), IsNextJS: detected, ExecutionError: message})
}
func FormatText(r *ScanResult) string {
	var b strings.Builder
	detected := r.DetectionStatus
	if detected == "" {
		detected = fmt.Sprint(r.IsNextJS)
	}
	fmt.Fprintf(&b, "Scan Results for: %s\nScan status: %s\nNext.js detection: %s\n", r.BaseURL, r.ScanStatus, detected)
	fmt.Fprintf(&b, "HTTP status: %d\nTLS verification disabled: %t\n", r.HTTPStatus, r.TLSVerificationDisabled)
	fmt.Fprintf(&b, "Detected Next.js Version: %s\nDetected React Runtime Version: %s\n", r.DetectedNextVersion, r.DetectedReactVersion)
	fmt.Fprintf(&b, "Router: %s\nBundler: %s\nAdapter: %s (version unknown)\nServing platform: %s\n", r.Router, r.Bundler, r.Adapter, r.Platform)
	fmt.Fprintf(&b, "Build ID: %s\nDeployment ID: %s\nAsset Prefix: %s\nCalculated Asset Base URL: %s\n", r.BuildID, r.DeploymentID, r.AssetPrefix, r.AssetBaseURL)
	fmt.Fprintf(&b, "Observed asset prefixes: %q\nBuild Manifest: %s\n", r.ObservedAssetPrefixes, r.ManifestStatus)
	for _, hop := range r.Redirects {
		fmt.Fprintf(&b, "Redirect: %d %s -> %s\n", hop.StatusCode, hop.URL, hop.Location)
	}
	if r.ExecutionError != nil {
		fmt.Fprintf(&b, "Execution Error: %v\n", r.ExecutionError)
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", warning)
	}
	keys := make([]string, 0, len(r.Routes))
	for key := range r.Routes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, manifest := range r.Manifests {
		fmt.Fprintf(&b, "Manifest source: %s (HTTP %d, %s)\n", manifest.URL, manifest.HTTPStatus, manifest.Status)
	}
	fmt.Fprintf(&b, "Manifest route patterns: %d (public URL prefixes/rewrites not inferred)\n", len(keys))
	for _, key := range keys {
		fmt.Fprintf(&b, "  %s (%d assets)\n", key, len(r.Routes[key]))
	}
	fmt.Fprintf(&b, "Discovered URLs: %d (not exhaustive)\n", len(r.DiscoveredURLs))
	for _, u := range r.DiscoveredURLs {
		fmt.Fprintf(&b, "  %s\n", u)
	}
	fmt.Fprintf(&b, "Manifest assets: %d\n", len(r.ManifestAssets))
	for _, inventory := range r.ChunkMaps {
		fmt.Fprintf(&b, "Webpack inventory: %d async chunks (not routes; not fetched), public path %s\n", len(inventory.Chunks), inventory.PublicPath)
	}
	fmt.Fprintf(&b, "Page observations: %d\n", len(r.PageObservations))
	for _, page := range r.PageObservations {
		fmt.Fprintf(&b, "  %s -> %s [%s, HTTP %d]: %d assets\n", page.RequestedURL, page.FinalURL, page.Technique, page.HTTPStatus, len(page.Assets))
	}
	fmt.Fprintf(&b, "Unique observed/manifest assets: %d\nEvidence:\n", len(r.AllAssets))
	for _, f := range r.Findings {
		if f.Property == "unattributed-version" {
			continue
		}
		fmt.Fprintf(&b, "  %s=%s [%s, %s] %s\n", f.Property, f.Value, f.Technique, f.Confidence, f.URL)
	}
	return b.String()
}
func output(r *ScanResult, format string) ([]byte, error) {
	switch format {
	case "json":
		return json.MarshalIndent(r, "", "  ")
	case "text":
		return []byte(FormatText(r)), nil
	default:
		return nil, fmt.Errorf("unknown output format: %s", format)
	}
}
func PrintResults(r *ScanResult, format string) error {
	data, e := output(r, format)
	if e != nil {
		return e
	}
	fmt.Println(string(data))
	return nil
}
func WriteOutput(r *ScanResult, file, format string) error {
	data, e := output(r, format)
	if e != nil {
		return e
	}
	return os.WriteFile(file, data, 0644)
}
