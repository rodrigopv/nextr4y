package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"github.com/fatih/color"

	"github.com/rodrigopv/nextr4y/internal/fetch"
	"github.com/rodrigopv/nextr4y/internal/versiondetect"
)

// Structure to hold extracted Next.js config data
type NextData struct {
	BuildID     string                 `json:"buildId"`
	AssetPrefix string                 `json:"assetPrefix"` 
	Props       map[string]interface{} `json:"props"`      
}

// Structure to hold the final results
type ScanResult struct {
	BaseURL         string
	AssetBaseURL    string 
	IsNextJS        bool
	BuildID         string
	AssetPrefix     string
	Routes          map[string][]string 
	AllAssets       map[string]bool     
	ManifestFound   bool
	ManifestExecOK  bool
	ExecutionError  error
	NextDataJSONRaw string 
	DetectedNextVersion string
	DetectedReactVersion string
}

// Scanner encapsulates the dependencies and logic for scanning a Next.js site.
type Scanner struct {
	fetcher         fetch.Fetcher
	versionDetector versiondetect.VersionDetector
	customBaseURL   string // Custom base URL provided by CLI parameter
}

// NewScanner creates a new Scanner with the required dependencies.
func NewScanner(fetcher fetch.Fetcher, detector versiondetect.VersionDetector, customBaseURL string) *Scanner {
	return &Scanner{
		fetcher:         fetcher,
		versionDetector: detector,
		customBaseURL:   customBaseURL,
	}
}

const userAgent = "go-nextr4y/1.0"

var manifestJSRegex = regexp.MustCompile(`self\.__BUILD_MANIFEST\s*=\s*(function\s*\(.*?\)\s*\{[\s\S]*?return\s*\{[\s\S]*?\}\s*\}\s*\(.*?\))`)
var simpleVersionRegex = regexp.MustCompile(`["'](\d+\.\d+\.\d+[^"']*)["']`)

// findInitialScriptURLs parses HTML content to find <script> tags pointing to Next.js JS chunks.
// It resolves the URLs relative to the provided assetBaseURL.
func findInitialScriptURLs(htmlContent string, assetBaseURL *url.URL) map[string]bool {
	jsURLs := make(map[string]bool)
	if assetBaseURL == nil {
		log.Println("Warning: Cannot resolve initial script URLs without an asset base URL.")
		return jsURLs
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("Warning: Failed to parse HTML for initial scripts: %v", err)
		return jsURLs
	}

	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if !exists || src == "" {
			return
		}

		if strings.Contains(src, "/_next/static/") {
			srcURL, err := url.Parse(src)
			if err != nil {
				log.Printf("Warning: Could not parse script src '%s': %v", src, err)
				return
			}

			if strings.HasSuffix(srcURL.Path, ".js") {
				fullURL := assetBaseURL.ResolveReference(srcURL).String()
				jsURLs[fullURL] = true
			}
		}
	})

	log.Printf("Found %d potential initial JS chunk URLs in HTML (resolved against asset base).", len(jsURLs))
	return jsURLs
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
		return nil, "", errors.New("__NEXT_DATA__ script tag not found")
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
			if assetStr, okStr := assetsInterface.(string); okStr && (strings.HasSuffix(assetStr, ".js") || strings.HasSuffix(assetStr, ".css")) {
				assetList = []interface{}{assetStr}
			} else {
				log.Printf("Warning: Skipping route '%s', expected asset list (array) but got %T", routePath, assetsInterface)
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

			if !strings.HasSuffix(assetPath, ".js") && !strings.HasSuffix(assetPath, ".css") {
				continue
			}

			assetPath = strings.TrimPrefix(assetPath, "/")
			
			fullPath := path.Join(baseURLParsed.Path, "_next", assetPath)
			
			resolvedURL := &url.URL{
				Scheme: baseURLParsed.Scheme,
				Host:   baseURLParsed.Host,
				Path:   fullPath,
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

// ScanTarget performs the Next.js analysis on the given target URL.
func (s *Scanner) ScanTarget(initialTargetURL string) (*ScanResult, error) {
	targetURL := initialTargetURL
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}
	log.Printf("Scanning target: %s", targetURL)

	htmlBodyReader, finalURL, fetchErr := s.fetcher.Fetch(targetURL)
	if fetchErr != nil {
		parsedBaseUrl, _ := url.Parse(targetURL)
		result := ScanResult{
			BaseURL:   targetURL,
			Routes:    make(map[string][]string),
			AllAssets: make(map[string]bool),
		}
		if parsedBaseUrl != nil {
			result.AssetBaseURL = parsedBaseUrl.String()
		}
		result.ExecutionError = fmt.Errorf("scanner: initial fetch failed for %s: %w", targetURL, fetchErr)
		return &result, result.ExecutionError
	}
	defer htmlBodyReader.Close()
	log.Printf("Initial fetch successful, final URL: %s", finalURL)

	baseURL, parseErr := url.Parse(finalURL)
	if parseErr != nil {
		result := ScanResult{
			BaseURL:   initialTargetURL,
			Routes:    make(map[string][]string),
			AllAssets: make(map[string]bool),
		}
		err := fmt.Errorf("scanner: invalid final URL '%s' received from fetcher: %w", finalURL, parseErr)
		result.ExecutionError = err
		return &result, err
	}

	result := ScanResult{
		BaseURL:   baseURL.String(),
		Routes:    make(map[string][]string),
		AllAssets: make(map[string]bool),
	}

	bodyBytes, readErr := io.ReadAll(htmlBodyReader)
	if readErr != nil {
		result.ExecutionError = fmt.Errorf("scanner: failed to read response body from %s: %w", finalURL, readErr)
		return &result, result.ExecutionError
	}
	htmlContent := string(bodyBytes)

	var nextData *NextData
	var nextDataErr error
	nextData, result.NextDataJSONRaw, nextDataErr = findAndParseNextData(strings.NewReader(htmlContent))

	if nextDataErr != nil {
		log.Printf("Note: Error processing __NEXT_DATA__: %v", nextDataErr)
		if nextData != nil && nextData.BuildID != "" {
			result.IsNextJS = true
			result.BuildID = nextData.BuildID
			result.AssetPrefix = nextData.AssetPrefix
		} else if !errors.Is(nextDataErr, errors.New("__NEXT_DATA__ script tag not found")) {
			result.IsNextJS = false
		}
	} else {
		result.IsNextJS = true
		result.BuildID = nextData.BuildID
		result.AssetPrefix = nextData.AssetPrefix
	}

	// Handle asset base URL based on whether a custom base URL was provided
	var assetBaseParsedURL url.URL
	
	if s.customBaseURL != "" {
		// Use the custom base URL when provided
		customURL, err := url.Parse(s.customBaseURL)
		if err != nil {
			log.Printf("Warning: Could not parse custom base URL '%s': %v. Using default behavior.", s.customBaseURL, err)
			assetBaseParsedURL = *baseURL
		} else {
			log.Printf("Using custom base URL: %s", s.customBaseURL)
			assetBaseParsedURL = *customURL
			
			// If asset prefix is detected, append it to the custom base URL
			if result.AssetPrefix != "" {
				prefixURL, err := url.Parse(result.AssetPrefix)
				if err == nil && prefixURL.IsAbs() {
					// If asset prefix is absolute, use just its path with the custom base URL
					prefixPath := prefixURL.Path
					if !strings.HasSuffix(assetBaseParsedURL.Path, "/") && !strings.HasPrefix(prefixPath, "/") {
						assetBaseParsedURL.Path += "/"
					}
					assetBaseParsedURL.Path += strings.TrimPrefix(prefixPath, "/")
					log.Printf("Appending absolute AssetPrefix path to custom base URL: %s", assetBaseParsedURL.String())
				} else {
					// For relative asset prefix, simply append it to the custom base path
					if !strings.HasSuffix(assetBaseParsedURL.Path, "/") && !strings.HasPrefix(result.AssetPrefix, "/") {
						assetBaseParsedURL.Path += "/"
					}
					assetBaseParsedURL.Path += strings.TrimPrefix(result.AssetPrefix, "/")
					log.Printf("Appending relative AssetPrefix to custom base URL: %s", assetBaseParsedURL.String())
				}
			}
		}
	} else {
		// Use the original logic when no custom base URL is provided
		assetBaseParsedURL = *baseURL
		if result.AssetPrefix != "" {
			prefixURL, err := url.Parse(result.AssetPrefix)
			if err == nil && prefixURL.IsAbs() {
				assetBaseParsedURL = *prefixURL
				log.Printf("Using absolute AssetPrefix: %s", assetBaseParsedURL.String())
			} else {
				assetPrefixURL := &url.URL{Path: result.AssetPrefix}
				resolvedAssetBaseURL := baseURL.ResolveReference(assetPrefixURL)
				assetBaseParsedURL = *resolvedAssetBaseURL
				if assetBaseParsedURL.Path != "" && !strings.HasSuffix(assetBaseParsedURL.Path, "/") {
					assetBaseParsedURL.Path += "/"
				}
				log.Printf("Using relative AssetPrefix, resolved asset base: %s", assetBaseParsedURL.String())
			}
		} else {
			log.Printf("No AssetPrefix found, asset paths will be resolved relative to page base: %s", assetBaseParsedURL.String())
		}
	}
	
	result.AssetBaseURL = assetBaseParsedURL.String()

	initialScriptURLs := findInitialScriptURLs(htmlContent, &assetBaseParsedURL)

	if errors.Is(nextDataErr, errors.New("__NEXT_DATA__ script tag not found")) && len(initialScriptURLs) > 0 {
		log.Println("__NEXT_DATA__ not found, but initial Next.js scripts detected. Setting IsNextJS=true.")
		result.IsNextJS = true
	}

	manifestAssets := make(map[string]bool)
	routes := make(map[string][]string)
	var manifestProcessingError error

	if result.BuildID != "" {
		// Construct the manifest URL correctly
		// When using custom base URL + asset prefix, we need to be careful with path construction
		// We need to determine whether _next should be a part of the asset prefix or appended separately
		
		var manifestURL string
		
		// Check if the asset base already contains _next in the path (from the asset prefix)
		if strings.Contains(assetBaseParsedURL.Path, "/_next/") || strings.HasSuffix(assetBaseParsedURL.Path, "/_next") {
			// Asset base already contains _next path, just append the rest
			relativePath := path.Join("static", result.BuildID, "_buildManifest.js")
			manifestPathURL := &url.URL{Path: relativePath}
			manifestURL = (&assetBaseParsedURL).ResolveReference(manifestPathURL).String()
		} else {
			// Asset base doesn't contain _next, so append the full path
			relativePath := path.Join("_next/static", result.BuildID, "_buildManifest.js")
			manifestPathURL := &url.URL{Path: relativePath}
			manifestURL = (&assetBaseParsedURL).ResolveReference(manifestPathURL).String()
		}
		
		log.Printf("Attempting to fetch build manifest from: %s", manifestURL)

		var manifestReader io.ReadCloser
		var manifestFinalURL string
		
		manifestReader, manifestFinalURL, fetchErr := s.fetcher.Fetch(manifestURL)
		if fetchErr != nil {
			log.Printf("Failed to fetch build manifest: %v", fetchErr)
			
			// Try a fallback approach for sites that might place the manifest at the root
			// This is especially relevant for complex CDN setups with custom base URLs
			var fallbackManifestURL string
			
			// Construct a fallback URL that assumes the manifest might be at the root of the asset prefix path
			customRoot, err := url.Parse(s.customBaseURL)
			if s.customBaseURL != "" && err == nil && result.AssetPrefix != "" {
				// Try a direct join of the host with _next path
				fallbackPath := path.Join("_next/static", result.BuildID, "_buildManifest.js")
				
				// Parse the asset prefix to extract host and path components
				prefixURL, prefixErr := url.Parse(result.AssetPrefix)
				
				if prefixErr == nil && prefixURL.Host != "" {
					// Handle case where AssetPrefix is a full URL
					host := prefixURL.Host
					pathPrefix := prefixURL.Path
					
					scheme := customRoot.Scheme
					if scheme == "" {
						scheme = "https" // Default to https
					}
					
					// Include the path component from the asset prefix
					completePath := path.Join(pathPrefix, fallbackPath)
					fallbackManifestURL = fmt.Sprintf("%s://%s%s", scheme, host, completePath)
				} else if strings.Contains(result.AssetPrefix, ".") {
					// Handle cases where the asset prefix might contain a domain
					// Extract domain and path components from the asset prefix
					assetPrefix := strings.Trim(result.AssetPrefix, "/")
					assetPrefixParts := strings.Split(assetPrefix, "/")
					
					// Check if the first part looks like a domain
					if len(assetPrefixParts) > 0 && strings.Contains(assetPrefixParts[0], ".") {
						host := assetPrefixParts[0]
						remainingPath := ""
						
						// Reconstruct the path after the domain
						if len(assetPrefixParts) > 1 {
							remainingPath = "/" + strings.Join(assetPrefixParts[1:], "/")
						}
						
						scheme := customRoot.Scheme
						if scheme == "" {
							scheme = "https" // Default to https
						}
						
						// Construct URL with domain and preserved path components
						completePath := path.Join(remainingPath, fallbackPath)
						fallbackManifestURL = fmt.Sprintf("%s://%s%s", scheme, host, completePath)
						log.Printf("Detected domain in AssetPrefix, using: %s://%s%s", scheme, host, remainingPath)
					} else {
						// If no domain detected, use the original error
						manifestProcessingError = fmt.Errorf("failed to fetch build manifest at %s: %w", manifestURL, fetchErr)
					}
				} else {
					manifestProcessingError = fmt.Errorf("failed to fetch build manifest at %s: %w", manifestURL, fetchErr)
				}
				
				// If we constructed a fallback URL, try to fetch it
				if fallbackManifestURL != "" {
					log.Printf("Trying fallback manifest location: %s", fallbackManifestURL)
					
					fallbackReader, fallbackFinalURL, fallbackErr := s.fetcher.Fetch(fallbackManifestURL)
					if fallbackErr == nil {
						// Successfully fetched the fallback URL
						manifestReader = fallbackReader
						manifestFinalURL = fallbackFinalURL
						fetchErr = nil // Clear the error since fallback worked
						log.Printf("Successfully fetched manifest from fallback location: %s", fallbackFinalURL)
					} else {
						log.Printf("Fallback manifest fetch also failed: %v", fallbackErr)
						// Keep the original error and continue with it
						manifestProcessingError = fmt.Errorf("failed to fetch build manifest at %s (and fallback): %w", manifestURL, fetchErr)
					}
				}
			} else {
				manifestProcessingError = fmt.Errorf("failed to fetch build manifest at %s: %w", manifestURL, fetchErr)
			}
		}
		
		// If we have a valid manifest reader (either from primary or fallback URL), process it
		if manifestReader != nil {
			defer manifestReader.Close()
			if manifestFinalURL != manifestURL {
				log.Printf("Build manifest request resulted in final URL: %s", manifestFinalURL)
			}
			result.ManifestFound = true

			manifestBytes, readErr := io.ReadAll(manifestReader)
			if readErr != nil {
				log.Printf("Failed to read build manifest: %v", readErr)
				manifestProcessingError = fmt.Errorf("failed to read build manifest from %s: %w", manifestFinalURL, readErr)
			} else {
				manifestJS := string(manifestBytes)
				execData, execErr := executeManifestJS(manifestJS)
				if execErr != nil {
					log.Printf("Failed to execute build manifest JS: %v", execErr)
					trimmedJS := strings.ReplaceAll(manifestJS, "\n", " ")
					if len(trimmedJS) > 200 { trimmedJS = trimmedJS[:200] + "..." }
					log.Printf("Problematic Manifest JS (preview): %s", trimmedJS)
					manifestProcessingError = fmt.Errorf("goja execution failed: %w", execErr)
				} else {
					result.ManifestExecOK = true
					routes, manifestAssets = extractRoutesAndAssets(execData, result.AssetBaseURL)
					result.Routes = routes
					result.AllAssets = manifestAssets
					log.Printf("Successfully processed build manifest. Found %d routes and %d assets.", len(routes), len(manifestAssets))
				}
			}
		}
	} else {
		log.Println("No BuildID found, skipping build manifest fetch.")
		if result.AllAssets == nil { result.AllAssets = make(map[string]bool) }
		for url := range initialScriptURLs {
			result.AllAssets[url] = true
		}
		log.Printf("No BuildID found. Using %d initial scripts for AllAssets.", len(initialScriptURLs))
	}

	combinedJSAssets := make(map[string]bool)
	for url := range initialScriptURLs {
		combinedJSAssets[url] = true
	}
	if result.ManifestFound && result.ManifestExecOK {
		for url := range manifestAssets {
			if strings.HasSuffix(url, ".js") {
				combinedJSAssets[url] = true
			}
		}
	}
	log.Printf("Using %d unique JS assets for version detection.", len(combinedJSAssets))

	nextV, reactV := s.versionDetector.Detect(result.BuildID, combinedJSAssets, &assetBaseParsedURL, s.fetcher)
	result.DetectedNextVersion = nextV
	result.DetectedReactVersion = reactV

	var finalError error
	if manifestProcessingError != nil {
		finalError = fmt.Errorf("scanner: manifest processing failed: %w", manifestProcessingError)
		log.Printf("Scan completed with manifest processing errors.")
	} else if nextDataErr != nil && !errors.Is(nextDataErr, errors.New("__NEXT_DATA__ script tag not found")) {
		finalError = fmt.Errorf("scanner: __NEXT_DATA__ processing error: %w", nextDataErr)
		log.Printf("Scan completed with __NEXT_DATA__ processing errors.")
	} else if errors.Is(nextDataErr, errors.New("__NEXT_DATA__ script tag not found")) && len(initialScriptURLs) == 0 {
		finalError = nextDataErr
		log.Printf("Scan complete: __NEXT_DATA__ not found and no initial scripts detected.")
	} else if errors.Is(nextDataErr, errors.New("__NEXT_DATA__ script tag not found")) && result.IsNextJS {
		log.Printf("Scan complete: __NEXT_DATA__ not found but initial scripts were present.")
	} else {
		log.Printf("Scan complete. Routes: %d, Assets (final combined): %d", len(result.Routes), len(combinedJSAssets))
	}

	if !result.IsNextJS {
		versionFound := result.DetectedNextVersion
		if versionFound != "" && !strings.HasPrefix(versionFound, "Unknown") && !strings.Contains(versionFound, "Likely") {
			log.Printf("Setting IsNextJS=true based on detected version '%s' despite missing __NEXT_DATA__.", versionFound)
			result.IsNextJS = true
			if finalError != nil && errors.Is(finalError, errors.New("__NEXT_DATA__ script tag not found")) {
				finalError = nil
			} else if finalError != nil && strings.Contains(finalError.Error(), "Unsupported deployment") {
				finalError = nil
			}
		}
	}

	result.ExecutionError = finalError

	return &result, finalError
}

// PrintResults formats and prints the scan results.
func PrintResults(result *ScanResult, outputFormat string) error {
	switch outputFormat {
	case "json":
		outJSON, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal result to JSON: %w", err)
		}
		fmt.Println(string(outJSON))
	case "text":
		// Define colors (will automatically handle non-TTY environments)
		title := color.New(color.FgWhite, color.Bold).SprintfFunc()
		label := color.New(color.FgYellow).SprintFunc()
		value := color.New(color.FgCyan).SprintFunc()
		valBoolTrue := color.New(color.FgGreen).SprintFunc()
		valBoolFalse := color.New(color.FgRed).SprintFunc()
		errorText := color.New(color.FgRed).SprintFunc()
		routePath := color.New(color.FgMagenta).SprintFunc()
		assetCount := color.New(color.FgBlue).SprintfFunc()

		fmt.Printf("%s: %s\n", title("Scan Results for"), value(result.BaseURL))
		fmt.Printf("%s %s\n", label("Is Next.js:"), formatBool(result.IsNextJS, valBoolTrue, valBoolFalse))

		if result.IsNextJS {
			fmt.Printf("%s %s\n", label("Build ID:"), value(result.BuildID))
			fmt.Printf("%s %s\n", label("Detected Next.js Version:"), value(result.DetectedNextVersion))
			fmt.Printf("%s %s\n", label("Detected React Version:"), value(result.DetectedReactVersion))
			fmt.Printf("%s %s\n", label("Asset Prefix:"), value(result.AssetPrefix))
			fmt.Printf("%s %s\n", label("Calculated Asset Base URL:"), value(result.AssetBaseURL))
			fmt.Printf("%s %s\n", label("Build Manifest Found:"), formatBool(result.ManifestFound, valBoolTrue, valBoolFalse))
			fmt.Printf("%s %s\n", label("Build Manifest Executed OK:"), formatBool(result.ManifestExecOK, valBoolTrue, valBoolFalse))

			if result.ExecutionError != nil {
				fmt.Printf("%s %s\n", label("Execution Error:"), errorText("\n"+result.ExecutionError.Error()))
			} else {
				fmt.Printf("%s (%s routes found):\n", label("Routes"), value(len(result.Routes)))
				routeKeys := make([]string, 0, len(result.Routes))
				for route := range result.Routes {
					routeKeys = append(routeKeys, route)
				}
				sort.Strings(routeKeys)

				for _, route := range routeKeys {
					assetNumStr := assetCount("(%d assets)", len(result.Routes[route]))
					fmt.Printf("  - %s %s\n", routePath(route), assetNumStr)
				}
				fmt.Printf("%s %s unique assets from manifest.\n", label("Found"), value(len(result.AllAssets)))
			}
		}
		if result.NextDataJSONRaw != "" && !result.IsNextJS {
			fmt.Printf("\n%s\n%s\n", label("Raw __NEXT_DATA__ (found but potentially invalid):"), result.NextDataJSONRaw)
		}
	default:
		return fmt.Errorf("unknown output format: %s", outputFormat)
	}
	return nil
}

// formatBool helper for colorizing boolean output
func formatBool(b bool, trueColorFunc, falseColorFunc func(a ...interface{}) string) string {
	if b {
		return trueColorFunc("true")
	}
	return falseColorFunc("false")
}

// WriteOutput formats and writes the scan results to a file.
// It defaults to JSON but can write text if specified.
func WriteOutput(result *ScanResult, outputFile string, outputFormat string) error {
	var outputBytes []byte
	var err error

	if outputFormat == "json" {
		outputBytes, err = json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal result to JSON for file output: %w", err)
		}
	} else if outputFormat == "text" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Scan Results for: %s\n", result.BaseURL))
		sb.WriteString(fmt.Sprintf("Is Next.js: %t\n", result.IsNextJS))
		if result.IsNextJS {
			sb.WriteString(fmt.Sprintf("Build ID: %s\n", result.BuildID))
			sb.WriteString(fmt.Sprintf("Detected Next.js Version: %s\n", result.DetectedNextVersion))
			sb.WriteString(fmt.Sprintf("Detected React Version: %s\n", result.DetectedReactVersion))  
			sb.WriteString(fmt.Sprintf("Asset Prefix: %s\n", result.AssetPrefix))
			sb.WriteString(fmt.Sprintf("Calculated Asset Base URL: %s\n", result.AssetBaseURL))
			sb.WriteString(fmt.Sprintf("Build Manifest Found: %t\n", result.ManifestFound))
			sb.WriteString(fmt.Sprintf("Build Manifest Executed OK: %t\n", result.ManifestExecOK))
			if result.ExecutionError != nil {
				sb.WriteString(fmt.Sprintf("Execution Error: %v\n", result.ExecutionError))
			} else {
				sb.WriteString(fmt.Sprintf("Found %d Routes:\n", len(result.Routes)))
				routeKeys := make([]string, 0, len(result.Routes))
				for route := range result.Routes {
					routeKeys = append(routeKeys, route)
				}
				sort.Strings(routeKeys)

				for _, route := range routeKeys {
					sb.WriteString(fmt.Sprintf("  - %s (%d assets)\n", route, len(result.Routes[route])))
				}
				sb.WriteString(fmt.Sprintf("Found %d Unique Assets from manifest.\n", len(result.AllAssets)))
			}
		}
		if result.NextDataJSONRaw != "" && !result.IsNextJS {
			sb.WriteString(fmt.Sprintf("\nRaw __NEXT_DATA__ (found but potentially invalid):\n%s\n", result.NextDataJSONRaw))
		}
		outputBytes = []byte(sb.String())
	} else {
		return fmt.Errorf("unknown output format for file writing: %s", outputFormat)
	}

	err = os.WriteFile(outputFile, outputBytes, 0644)
	if err != nil {
		return fmt.Errorf("failed to write output file '%s': %w", outputFile, err)
	}
	log.Printf("Results written to %s", outputFile)
	return nil
} 