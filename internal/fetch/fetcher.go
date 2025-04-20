package fetch

import (
	"io"
)

// FetcherCapabilities describes the optional abilities of a Fetcher implementation.
type FetcherCapabilities struct {
	CanExecuteJavaScript bool // Indicates if the fetcher can execute JavaScript on the page.
	CanQueryDOM          bool // Indicates if the fetcher can query the live DOM structure.
}

// Fetcher defines the contract for retrieving web content.
// Implementations are responsible for handling the specifics of fetching,
// including following redirects and returning the final URL.
type Fetcher interface {
	// Fetch retrieves the content from the targetURL.
	// It follows redirects and returns the content as an io.ReadCloser,
	// the final URL reached after any redirects, and an error if fetching failed.
	// The caller is responsible for closing the returned io.ReadCloser.
	Fetch(targetURL string) (content io.ReadCloser, finalURL string, err error)

	// Capabilities returns a description of the fetcher's optional abilities.
	Capabilities() FetcherCapabilities
} 