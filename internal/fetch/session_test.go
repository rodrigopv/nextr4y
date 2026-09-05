package fetch

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCookieRedirectSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test", Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/demo", 307)
			return
		}
		if _, e := r.Cookie("session"); e != nil {
			http.Redirect(w, r, "/auth", 307)
			return
		}
		w.Header().Set("X-Opennext", "1")
		w.Write([]byte("ready"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher()
	res, e := f.FetchResponse(Request{URL: srv.URL + "/demo"})
	require.NoError(t, e)
	require.Equal(t, 200, res.StatusCode)
	require.Len(t, res.Redirects, 2)
	require.Equal(t, "ready", string(res.Body))
	require.Equal(t, "1", res.Headers.Get("X-Opennext"))
	require.Empty(t, res.Headers.Get("Set-Cookie"))
	res, e = f.FetchResponse(Request{URL: srv.URL + "/asset"})
	require.NoError(t, e)
	require.Empty(t, res.Redirects)
}
func TestRedirectLoopRetainsHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 307) }))
	defer srv.Close()
	res, e := NewHTTPFetcher().FetchResponse(Request{URL: srv.URL + "/loop"})
	require.ErrorContains(t, e, "redirect loop")
	require.Len(t, res.Redirects, 1)
}
func TestNon200AndRequestHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("RSC") == "1" {
			w.Header().Set("Content-Type", "text/x-component")
			w.Write([]byte(`0:{"b":"build","f":[]}`))
			return
		}
		w.Header().Set("X-Powered-By", "Next.js")
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher()
	res, e := f.FetchResponse(Request{URL: srv.URL})
	require.NoError(t, e)
	require.Equal(t, 404, res.StatusCode)
	require.Equal(t, "not found", string(res.Body))
	res, e = f.FetchResponse(Request{URL: srv.URL, Headers: http.Header{"Rsc": []string{"1"}}})
	require.NoError(t, e)
	require.Equal(t, "text/x-component", res.Headers.Get("Content-Type"))
}
func TestTLSVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	_, e := NewHTTPFetcher().FetchResponse(Request{URL: srv.URL})
	require.Error(t, e)
	res, e := NewHTTPFetcherWithOptions(true).FetchResponse(Request{URL: srv.URL})
	require.NoError(t, e)
	require.Equal(t, 200, res.StatusCode)
	require.True(t, res.TLSVerificationDisabled)
}
func TestCrossOriginRedirectStripsAuthorization(t *testing.T) {
	var got string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization"); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	_, e := NewHTTPFetcher().FetchResponse(Request{URL: source.URL, Headers: http.Header{"Authorization": []string{"Bearer test"}}})
	require.NoError(t, e)
	require.Empty(t, got)
}

func TestDefaultAcceptAndCallerOverride(t *testing.T) {
	for _, accept := range []string{"", "text/x-component"} {
		t.Run("accept="+accept, func(t *testing.T) {
			want := accept
			if want == "" {
				want = "*/*"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != want {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			headers := make(http.Header)
			if accept != "" {
				headers.Set("Accept", accept)
			}
			res, err := NewHTTPFetcher().FetchResponse(Request{URL: srv.URL, Headers: headers})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, accept, headers.Get("Accept"), "caller headers must not be mutated")
		})
	}
}
