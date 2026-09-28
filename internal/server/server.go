// Package server assembles the HTTP handler. Every route but liveness authenticates
// (persistence-api.md §10); liveness returns no data.
package server

import (
	"net/http"
	"time"
)

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// NewHTTP returns the server for addr, serving New().
//
// ReadTimeout bounds the whole request, body included: without it, a request that declares a
// body and never sends one holds its connection while net/http drains it after the response.
// IdleTimeout bounds kept-alive connections. WriteTimeout stays unset: the event stream
// (persistence-api.md §8.3) writes for longer than any fixed bound. The general OPTIONS handler
// is disabled so `OPTIONS *` reaches the mux instead of being answered 200 without authentication.
func NewHTTP(addr string) *http.Server {
	return &http.Server{
		Addr:                         addr,
		Handler:                      New(),
		ReadHeaderTimeout:            10 * time.Second,
		ReadTimeout:                  30 * time.Second,
		IdleTimeout:                  2 * time.Minute,
		DisableGeneralOptionsHandler: true,
	}
}
