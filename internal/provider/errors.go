package provider

import (
	"errors"
	"fmt"
)

// The typed outcomes of a provider request. An error from this package is at most one of them;
// one that is none is a status this client does not classify. errors.Is tells them apart.
var (
	// ErrDenied is a 403: the identity's policy refused the request, or its token is invalid
	// or expired (no renewal in the PoC; an expired token is refused like any denial).
	ErrDenied = errors.New("refused by the provider")
	// ErrExists is KV v2's check-and-set refusal of a cas=0 create: the path already holds a
	// version.
	ErrExists = errors.New("the generation already exists")
	// ErrAbsent is a read's 404: the path holds no live version (never written, deleted, or
	// destroyed).
	ErrAbsent = errors.New("no secret at the path")
	// ErrUnavailable is a sealed, overloaded or unreachable provider, or a request whose
	// answer was lost. For a write the outcome is unknown: it may have landed.
	ErrUnavailable = errors.New("provider unavailable or the outcome unknown")
	// ErrProtocol is a response this client does not understand: a 2xx without its field, with
	// a field of the wrong type or form, or larger than the response cap.
	ErrProtocol = errors.New("provider response not understood")
)

// requestError is every failure of a request. Its text names the method, the path and the status,
// and otherwise only this package's own fixed words: never a request or response body, never
// server error text, never a transport or JSON error's text, any of which can quote what was sent
// or received (a server can echo a request in an error, a malformed response is quoted by
// net/http, a JSON syntax error quotes a character of the input).
type requestError struct {
	method, path string
	status       int    // 0 when no response was read
	kind         error  // one of the four, or nil
	detail       string // this package's fixed words
	cause        error  // a context error, for errors.Is; its text is fixed by the standard library
}

func (e *requestError) Error() string {
	s := "provider: " + e.method + " " + e.path
	if e.status != 0 {
		s += fmt.Sprintf(": status %d", e.status)
	}
	if e.kind != nil {
		s += ": " + e.kind.Error()
	}
	if e.detail != "" {
		s += ": " + e.detail
	}
	return s
}

func (e *requestError) Unwrap() []error {
	var errs []error
	if e.kind != nil {
		errs = append(errs, e.kind)
	}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	return errs
}
