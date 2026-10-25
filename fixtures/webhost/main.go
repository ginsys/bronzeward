// Command webhost is the web client's test static host (client contracts §10.1, §10.2): it serves
// the built web package with the response headers web/headers.json holds, proxies /api/v1/ and
// /livez to the server, and answers every other path with the application. With -redirect-api it
// answers every API request with a redirect to another origin instead (cases TLS-02 and TLS-03).
// Never a deployment host: packaging renders the deployment's configuration from the same file.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "webhost:", err)
		os.Exit(1)
	}
}

type config struct {
	dist, headers, clientConfig string
	issuer, providerOrigins     string
	api, redirectAPI            string
}

func run(args []string) error {
	fs := flag.NewFlagSet("webhost", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:58443", "listen address; the test provider's web origin is http://localhost:58443")
	var c config
	fs.StringVar(&c.dist, "dist", "web/dist", "the built web package")
	fs.StringVar(&c.headers, "headers", "web/headers.json", "the §10.2 header set")
	fs.StringVar(&c.clientConfig, "client-config", "", "file served as /bronzeward-client.json")
	fs.StringVar(&c.issuer, "issuer", "", "the issuer's origin, for form-action and connect-src")
	fs.StringVar(&c.providerOrigins, "provider-origins", "", "further connect-src origins, space-separated: the provider's token and revocation endpoints")
	fs.StringVar(&c.api, "api", "http://127.0.0.1:8080", "the server /api/v1/ and /livez go to")
	fs.StringVar(&c.redirectAPI, "redirect-api", "", "answer every /api/v1/ request with a 307 to this origin instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	h, err := handler(c)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// renderHeaders reads the header set and fills the CSP's two placeholders.
func renderHeaders(file, issuer, providerOrigins string) (map[string]string, error) {
	if issuer == "" {
		return nil, errors.New("-issuer is required: the CSP's form-action and connect-src name it")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	origins := strings.Join(strings.Fields(issuer+" "+providerOrigins), " ")
	for k, v := range headers {
		headers[k] = strings.NewReplacer("{providerOrigins}", origins, "{issuerOrigin}", issuer).Replace(v)
	}
	return headers, nil
}

func handler(c config) (http.Handler, error) {
	headers, err := renderHeaders(c.headers, c.issuer, c.providerOrigins)
	if err != nil {
		return nil, err
	}
	upstream, err := url.Parse(c.api)
	if err != nil {
		return nil, fmt.Errorf("-api: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	pages := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		name := filepath.Join(c.dist, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		if r.URL.Path == "/bronzeward-client.json" && c.clientConfig != "" {
			name = c.clientConfig
		} else if fi, err := os.Stat(name); err != nil || fi.IsDir() {
			name = filepath.Join(c.dist, "index.html") // every other path is the application (B3)
		}
		f, err := os.Open(name)
		if err != nil {
			http.Error(w, "not built", http.StatusNotFound)
			return
		}
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, fi.Name(), time.Time{}, f)
	})
	// No ServeMux: its path-cleaning redirects would answer a page without the headers.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/livez", strings.HasPrefix(r.URL.Path, "/api/v1/") && c.redirectAPI == "":
			proxy.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/v1/"):
			http.Redirect(w, r, strings.TrimSuffix(c.redirectAPI, "/")+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		default:
			pages.ServeHTTP(w, r)
		}
	}), nil
}
