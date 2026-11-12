// Package client is bronzewardctl's request core: contexts, TLS, credentials, errors, pagination
// and epochs, under the client contracts (docs/spec/client-contracts.md). Its request and response
// types are internal/client/apitypes, generated from api/openapi.yaml. closure_test.go checks that
// neither links the server's API, database, provider or Talos packages, or the PostgreSQL driver.
// Two exceptions: internal/talos/discover, a read-only package, and internal/accessput, a
// write-only package that sends one KV create to OpenBao with the operator's own token. Neither
// imports internal/talos, internal/provider or a client library.
package client
