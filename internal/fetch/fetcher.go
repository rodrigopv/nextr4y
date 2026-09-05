package fetch

import (
	"fmt"
	"io"
	"net/http"
)

const MaxBodyBytes = 16 << 20

type FetcherCapabilities struct {
	CanExecuteJavaScript bool
	CanQueryDOM          bool
}

// Fetcher remains compatible with existing independently implemented probes.
type Fetcher interface {
	Fetch(string) (io.ReadCloser, string, error)
	Capabilities() FetcherCapabilities
}

type Request struct {
	URL     string
	Headers http.Header
}
type Redirect struct {
	URL        string
	StatusCode int
	Location   string
}
type Response struct {
	URL                     string
	StatusCode              int
	Headers                 http.Header
	Body                    []byte
	Redirects               []Redirect
	TLSVerificationDisabled bool
}

// ResponseFetcher exposes HTTP evidence even for non-200 responses.
type ResponseFetcher interface {
	FetchResponse(Request) (*Response, error)
}

func Read(f Fetcher, req Request) (*Response, error) {
	if detailed, ok := f.(ResponseFetcher); ok {
		return detailed.FetchResponse(req)
	}
	if len(req.Headers) > 0 {
		return nil, fmt.Errorf("fetcher does not support request headers")
	}
	body, final, err := f.Fetch(req.URL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBodyBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", MaxBodyBytes)
	}
	return &Response{URL: final, StatusCode: 200, Headers: make(http.Header), Body: data}, nil
}
