package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const headersFile = "../../web/headers.json"

// The header set client contracts §10.2 requires, with the issuer http://idp.test and the provider
// origin http://token.test filled in.
var section102 = map[string]string{
	"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; " +
		"connect-src 'self' http://idp.test http://token.test; form-action http://idp.test; frame-ancestors 'none'; " +
		"base-uri 'none'; require-trusted-types-for 'script'",
	"X-Content-Type-Options":     "nosniff",
	"Referrer-Policy":            "no-referrer",
	"Cross-Origin-Opener-Policy": "same-origin",
	"Cache-Control":              "no-store",
}

// host serves a fake build, its upstream counting each request it gets.
func host(t *testing.T, redirect string) (*httptest.Server, *int) {
	t.Helper()
	dist := t.TempDir()
	for name, body := range map[string]string{"index.html": "<!doctype html>app", "assets/index-x.js": "js"} {
		p := filepath.Join(dist, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clientConfig := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(clientConfig, []byte(`{"issuer":"http://idp.test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hits := new(int)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Header().Set("X-Upstream", r.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	h, err := handler(config{dist: dist, headers: headersFile, clientConfig: clientConfig, issuer: "http://idp.test",
		providerOrigins: "http://token.test", api: upstream.URL, redirectAPI: redirect})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, hits
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}

// TestHeadersOnEveryPage: the file is exactly §10.2's set, and every page the host serves, the
// application fallback included, carries all of it.
func TestHeadersOnEveryPage(t *testing.T) {
	headers, err := renderHeaders(headersFile, "http://idp.test", "http://token.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != len(section102) {
		t.Errorf("%s holds %d headers, want §10.2's %d", headersFile, len(headers), len(section102))
	}
	srv, _ := host(t, "")
	for path, want := range map[string]string{
		"/": "<!doctype html>app", "/machines/m1": "<!doctype html>app", "/callback": "<!doctype html>app",
		"/signed-out": "<!doctype html>app", "/assets/": "<!doctype html>app", "/../index.html": "<!doctype html>app",
		"/assets/index-x.js": "js", "/bronzeward-client.json": `{"issuer":"http://idp.test"}`,
	} {
		res, body := get(t, srv.URL+path)
		if res.StatusCode != http.StatusOK || body != want {
			t.Errorf("%s: %d %q, want 200 %q", path, res.StatusCode, body, want)
		}
		for k, v := range section102 {
			if got := res.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
	}
}

func TestProxiesTheAPIAndLiveness(t *testing.T) {
	srv, hits := host(t, "")
	for _, path := range []string{"/api/v1/server", "/api/v1/machines?cluster=c1", "/livez"} {
		res, _ := get(t, srv.URL+path)
		if res.StatusCode != http.StatusNoContent || res.Header.Get("X-Upstream") != path {
			t.Errorf("%s: %d, upstream saw %q", path, res.StatusCode, res.Header.Get("X-Upstream"))
		}
	}
	if *hits != 3 {
		t.Errorf("upstream got %d requests, want 3", *hits)
	}
}

func TestRedirectInjection(t *testing.T) {
	srv, hits := host(t, "http://127.0.0.1:58444/")
	res, _ := get(t, srv.URL+"/api/v1/server?x=1")
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "http://127.0.0.1:58444/api/v1/server?x=1" {
		t.Errorf("got %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
	if *hits != 0 {
		t.Errorf("upstream got %d requests, want none", *hits)
	}
}

func TestIssuerRequired(t *testing.T) {
	if _, err := renderHeaders(headersFile, "", ""); err == nil {
		t.Error("no error without an issuer")
	}
}
