// Package server assembles the HTTP handler. Every route but liveness authenticates
// (persistence-api.md §10); liveness returns no data.
package server

import "net/http"

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
