package fetch

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Danny-Dasilva/CycleTLS/cycletls"
)

// tlsProfile holds a JA3 fingerprint and User-Agent combination.
type tlsProfile struct {
	ja3       string
	userAgent string
}

// defaultProfiles defines the list of profiles to try sequentially.
var defaultProfiles = []tlsProfile{
	{
		// Safari on macos
		ja3:       "772,4865-4866-4867-49196-49195-52393-49200-49199-52392-49162-49161-49172-49171-157-156-53-47-49160-49170-10,0-23-65281-10-11-16-5-13-18-51-45-43-27,29-23-24-25,0",
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15",
	},
	{
		// Default Firefox profile
		ja3:       "771,4865-4867-4866-49195-49199-52393-52392-49196-49200-49162-49161-49171-49172-51-57-47-53-10,0-23-65281-10-11-35-16-5-51-43-13-45-28-21,29-23-24-25-256-257,0",
		userAgent: "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:87.0) Gecko/20100101 Firefox/87.0",
	},
}

// HTTPFetcher implements the Fetcher interface using cycleTLS.
type HTTPFetcher struct {
	client   cycletls.CycleTLS
	profiles []tlsProfile
}

// NewHTTPFetcher creates a new HTTPFetcher with default cycleTLS settings and profiles.
func NewHTTPFetcher() *HTTPFetcher {
	client := cycletls.Init()
	return &HTTPFetcher{
		client:   client,
		profiles: defaultProfiles,
	}
}

// Fetch retrieves the content from the targetURL using cycleTLS.
// It iterates through a list of predefined JA3/User-Agent profiles,
// attempting the request with each until one succeeds or the list is exhausted.
// It returns the content as an io.ReadCloser, the final URL reached
// after any redirects, and an error if fetching failed.
// The caller is responsible for closing the returned io.ReadCloser.
func (f *HTTPFetcher) Fetch(targetURL string) (io.ReadCloser, string, error) {
	var lastResp cycletls.Response
	var lastErr error
	var success bool
	var finalURL string

	for i, profile := range f.profiles {
		options := cycletls.Options{
			Body:      "",
			Ja3:       profile.ja3,
			UserAgent: profile.userAgent,
			Headers:   map[string]string{},
		}

		resp, err := f.client.Do(targetURL, options, "GET")

		lastResp = resp
		lastErr = err

		if err != nil {
			fmt.Printf("http_fetcher: Profile #%d failed for %s: Error during Do(): %v\n", i+1, targetURL, err)
			continue
		}

		if resp.Status == 0 && (strings.Contains(resp.Body, "tls: protocol version not supported") || strings.Contains(resp.Body, "HANDSHAKE_FAILURE")) {
			fmt.Printf("http_fetcher: Profile #%d failed for %s: TLS handshake error. Body: %s\n", i+1, targetURL, resp.Body)
			continue
		}

		if resp.Status == http.StatusForbidden {
			fmt.Printf("http_fetcher: Profile #%d received 403 Forbidden for %s. Trying next profile.\n", i+1, targetURL)
			continue
		}

		success = true
		break
	}

	if !success {
		errMsg := fmt.Sprintf("http_fetcher: all TLS profiles failed for %s", targetURL)
		if lastErr != nil {
			errMsg = fmt.Sprintf("%s. Last Do() error: %v", errMsg, lastErr)
		} else if lastResp.Status == 0 && lastResp.Body != "" {
			errMsg = fmt.Sprintf("%s. Last response body: %s", errMsg, lastResp.Body)
		} else if lastResp.Status == http.StatusForbidden {
			errMsg = fmt.Sprintf("%s. Last attempt resulted in 403 Forbidden.", errMsg)
		}
		finalURL = lastResp.FinalUrl
		if finalURL == "" {
			finalURL = targetURL
		}
		return nil, finalURL, fmt.Errorf("%s", errMsg)
	}

	finalURL = lastResp.FinalUrl
	if finalURL == "" {
		finalURL = targetURL
	}

	if lastResp.Status == 0 {
		errMsg := fmt.Sprintf("http_fetcher: cycleTLS returned status 0 (non-TLS handshake error) for %s", finalURL)
		if lastResp.Body != "" {
			errMsg = fmt.Sprintf("%s, body: %s", errMsg, lastResp.Body)
		}
		return nil, finalURL, fmt.Errorf("%s", errMsg)
	}

	if lastResp.Status != http.StatusOK {
		return nil, finalURL, fmt.Errorf("http_fetcher: bad status code fetching %s (final URL: %s): %d", targetURL, finalURL, lastResp.Status)
	}

	bodyReader := strings.NewReader(lastResp.Body)
	bodyCloser := io.NopCloser(bodyReader)

	return bodyCloser, finalURL, nil
}

// Capabilities implements the Fetcher interface.
// cycleTLS mimics browser TLS but doesn't execute JS or parse DOM.
func (f *HTTPFetcher) Capabilities() FetcherCapabilities {
	return FetcherCapabilities{
		CanExecuteJavaScript: false,
		CanQueryDOM:          false,
	}
} 