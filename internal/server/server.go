// Package server assembles the HTTP handler. Every route but liveness authenticates
// (persistence-api.md §10); liveness returns no data.
package server

import (
	"net/http"
	"time"
)

// New serves liveness without authentication and hands every other request to api, which
// authenticates it first (persistence-api.md §10). A nil api answers 404 (tests). No ServeMux
// sits in front of api: it would answer an unclean path with a redirect and `OPTIONS *` with
// its own 400, both before authentication.
func New(api http.Handler) http.Handler {
	if api == nil {
		api = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livez" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		api.ServeHTTP(w, r)
	})
}

// NewHTTP returns the server for addr, serving h.
//
// ReadTimeout bounds the whole request, body included: without it, a request that declares a
// body and never sends one holds its connection while net/http drains it after the response.
// IdleTimeout bounds kept-alive connections. WriteTimeout stays unset: the event stream
// (persistence-api.md §8.3) writes for longer than any fixed bound. The general OPTIONS handler
// is disabled so `OPTIONS *` reaches the handler instead of being answered 200 without authentication.
func NewHTTP(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:                         addr,
		Handler:                      h,
		ReadHeaderTimeout:            10 * time.Second,
		ReadTimeout:                  30 * time.Second,
		IdleTimeout:                  2 * time.Minute,
		DisableGeneralOptionsHandler: true,
	}
}
