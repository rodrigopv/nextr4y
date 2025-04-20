package fetch

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPFetcher_Fetch(t *testing.T) {
	t.Parallel() // Run top-level test in parallel if possible

	testCases := []struct {
		name                 string
		targetPath           string
		serverHandler        http.HandlerFunc
		expectFinalPath      string
		expectContent        string
		expectErrorSubstring string // If empty, no error is expected
	}{
		{
			name:       "Success - 200 OK",
			targetPath: "/success",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/success", r.URL.Path)
				fmt.Fprintln(w, "Success Body")
			},
			expectFinalPath:      "/success",
			expectContent:        "Success Body\n",
			expectErrorSubstring: "",
		},
		{
			name:       "Redirect - 302 Found",
			targetPath: "/redirect",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					// The http client resolves relative redirect paths against the server base.
					http.Redirect(w, r, "/final-destination", http.StatusFound)
				} else if r.URL.Path == "/final-destination" {
					fmt.Fprintln(w, "Redirected Content")
				} else {
					http.NotFound(w, r)
				}
			},
			expectFinalPath:      "/final-destination",
			expectContent:        "Redirected Content\n",
			expectErrorSubstring: "",
		},
		{
			name:       "Client Error - 404 Not Found",
			targetPath: "/notfound",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			expectFinalPath:      "/notfound",
			expectContent:        "",
			expectErrorSubstring: "bad status code fetching",
		},
		{
			name:       "Server Error - 500 Internal Server Error",
			targetPath: "/servererror",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			},
			expectFinalPath:      "/servererror",
			expectContent:        "",
			expectErrorSubstring: "bad status code fetching",
		},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		tc := tc // Capture range variable for parallel runs
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // Mark subtest for parallel execution

			server := httptest.NewServer(tc.serverHandler)
			defer server.Close()

			fetcher := NewHTTPFetcher()

			targetURL := server.URL + tc.targetPath
			expectedFinalURL := server.URL + tc.expectFinalPath

			contentReader, finalURL, err := fetcher.Fetch(targetURL)

			if tc.expectErrorSubstring != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectErrorSubstring)
				require.Nil(t, contentReader)
				if tc.expectFinalPath != "" { // Check finalURL even on error if one is expected
					require.Equal(t, expectedFinalURL, finalURL)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, contentReader)
				defer contentReader.Close() // Ensure close even if ReadAll fails

				bodyBytes, readErr := io.ReadAll(contentReader)
				require.NoError(t, readErr)

				require.Equal(t, tc.expectContent, string(bodyBytes))
				require.Equal(t, expectedFinalURL, finalURL)
			}
		})
	}
}

// Optional: Test NewHTTPFetcherWithClient if specific client behavior needs testing
// func TestNewHTTPFetcherWithClient(t *testing.T) { ... }

// Optional: Test error during request creation (if possible, e.g. invalid method)
// This is less about the Fetch method logic and more about http.NewRequest.
// func TestHTTPFetcher_Fetch_BadRequest(t *testing.T) { ... } 