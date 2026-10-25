// Package client is bronzewardctl's request core: contexts, TLS, credentials, errors, pagination
// and epochs, under the client contracts (docs/spec/client-contracts.md). Its request and response
// types are internal/client/apitypes, generated from api/openapi.yaml; neither package imports the
// server's packages, as closure_test.go checks.
package client
