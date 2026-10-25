// Package client is bronzewardctl's request core: contexts, TLS, credentials, errors, pagination
// and epochs, under the client contracts (docs/spec/client-contracts.md). Its request and response
// types are internal/client/apitypes, generated from api/openapi.yaml. closure_test.go checks that
// neither links the server's API, database, provider or Talos packages, or the PostgreSQL driver.
package client
