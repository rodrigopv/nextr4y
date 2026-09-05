package fetch

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/Danny-Dasilva/CycleTLS/cycletls"
	"golang.org/x/net/publicsuffix"
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

// HTTPFetcher keeps a browser-style session for the lifetime of a scan.
type HTTPFetcher struct {
	client             cycletls.CycleTLS
	profiles           []tlsProfile
	jar                http.CookieJar
	insecureSkipVerify bool
}

func NewHTTPFetcher() *HTTPFetcher { return NewHTTPFetcherWithOptions(false) }
func NewHTTPFetcherWithOptions(insecure bool) *HTTPFetcher {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &HTTPFetcher{client: cycletls.Init(), profiles: defaultProfiles, jar: jar, insecureSkipVerify: insecure}
}
func (f *HTTPFetcher) Fetch(target string) (io.ReadCloser, string, error) {
	res, err := f.FetchResponse(Request{URL: target})
	final := target
	if res != nil {
		final = res.URL
	}
	if err != nil {
		return nil, final, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, final, fmt.Errorf("http_fetcher: bad status code fetching %s: %d", final, res.StatusCode)
	}
	return io.NopCloser(strings.NewReader(string(res.Body))), final, nil
}

func (f *HTTPFetcher) FetchResponse(req Request) (*Response, error) {
	current, err := url.Parse(req.URL)
	if err != nil || current.Host == "" || (current.Scheme != "https" && current.Scheme != "http") {
		return nil, fmt.Errorf("invalid HTTP URL: %s", req.URL)
	}
	headers := req.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	// CycleTLS does not add Accept automatically. Some sites reject otherwise
	// valid requests without it; preserve explicit content negotiation (e.g. RSC).
	if headers.Get("Accept") == "" {
		headers.Set("Accept", "*/*")
	}
	var history []Redirect
	seen := make(map[string]bool)
	var result *Response
	for hop := 0; hop <= 10; hop++ {
		// URL alone is not a loop: an auth redirect may have changed the session.
		cookieHeader := &http.Request{Header: make(http.Header)}
		for _, c := range f.jar.Cookies(current) {
			cookieHeader.AddCookie(c)
		}
		state := fmt.Sprintf("%s:%x", current.String(), sha256.Sum256([]byte(cookieHeader.Header.Get("Cookie"))))
		if seen[state] {
			return result, fmt.Errorf("redirect loop at %s", current.Redacted())
		}
		seen[state] = true
		var raw cycletls.Response
		var requestErr error
		for index, profile := range f.profiles {
			h := make(map[string]string)
			for k, v := range headers {
				h[k] = strings.Join(v, ", ")
			}
			if cookie := cookieHeader.Header.Get("Cookie"); cookie != "" {
				h["Cookie"] = cookie
			}
			log.Printf("HTTP: GET %s (TLS profile %d/%d)", current.Redacted(), index+1, len(f.profiles))
			started := time.Now()
			raw, requestErr = f.client.Do(current.String(), cycletls.Options{
				Ja3: profile.ja3, UserAgent: profile.userAgent, Headers: h,
				DisableRedirect: true, Timeout: 20, InsecureSkipVerify: f.insecureSkipVerify,
			}, "GET")
			if requestErr != nil {
				log.Printf("HTTP: profile %d failed after %s: %v", index+1, time.Since(started).Round(time.Millisecond), requestErr)
			} else {
				log.Printf("HTTP: status %d in %s", raw.Status, time.Since(started).Round(time.Millisecond))
			}
			if requestErr == nil && raw.Status != 0 && raw.Status != http.StatusForbidden {
				break
			}
			if index+1 < len(f.profiles) {
				log.Println("HTTP: retrying with next TLS profile")
			}
		}
		if requestErr != nil {
			return result, fmt.Errorf("fetch %s: %w", current.Redacted(), requestErr)
		}
		if raw.Status == 0 {
			return result, fmt.Errorf("transport failure fetching %s: %s", current.Redacted(), raw.Body)
		}
		rh := make(http.Header)
		for k, v := range raw.Headers {
			for _, value := range strings.Split(v, "/,/") {
				rh.Add(k, value)
			}
		}
		f.jar.SetCookies(current, raw.Cookies)
		// Cookies remain in the jar, not in serializable scan evidence.
		rh.Del("Set-Cookie")
		result = &Response{URL: current.String(), StatusCode: raw.Status, Headers: rh,
			Redirects: append([]Redirect(nil), history...), TLSVerificationDisabled: f.insecureSkipVerify}
		// CycleTLS buffers responses internally; this bounds downstream parsing.
		if len(raw.Body) > MaxBodyBytes {
			return result, fmt.Errorf("response exceeds %d bytes", MaxBodyBytes)
		}
		result.Body = []byte(raw.Body)
		switch raw.Status {
		case 301, 302, 303, 307, 308:
		default:
			return result, nil
		}
		location := rh.Get("Location")
		if location == "" {
			return result, nil
		}
		next, e := current.Parse(location)
		if e != nil || next.Host == "" || (next.Scheme != "http" && next.Scheme != "https") {
			return result, fmt.Errorf("invalid redirect from %s", current.Redacted())
		}
		log.Printf("HTTP: redirect %d -> %s", raw.Status, next.Redacted())
		history = append(history, Redirect{URL: current.String(), StatusCode: raw.Status, Location: next.String()})
		result.Redirects = append([]Redirect(nil), history...)
		if next.Host != current.Host || next.Scheme != current.Scheme {
			headers.Del("Authorization")
			headers.Del("Cookie")
			headers.Del("Proxy-Authorization")
		}
		current = next
	}
	return result, fmt.Errorf("stopped after 10 redirects")
}
func (f *HTTPFetcher) Capabilities() FetcherCapabilities { return FetcherCapabilities{} }
