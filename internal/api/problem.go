package api

import (
	"encoding/json"
	"net/http"
)

// titles holds every problem code this package answers (persistence-api.md §9.4), with its title.
var titles = map[string]string{
	"invalid-request":          "The request is malformed",
	"cursor-invalid":           "The cursor is not valid here",
	"unauthenticated":          "No valid credential",
	"forbidden":                "No qualifying role",
	"identity-revoked":         "The identity is revoked",
	"not-found":                "No such resource or route",
	"conflict":                 "The resource refuses the act in its current state",
	"idempotency-key-reused":   "The idempotency key was used for another request",
	"idempotency-key-required": "An Idempotency-Key header is required",
	"precondition-required":    "An If-Match header is required",
	"precondition-failed":      "The If-Match header does not match",
	"validation-failed":        "Compilation refused the input",
	"epoch-superseded":         "The server started before the current recovery epoch",
	"internal-error":           "The server failed",
	"not-implemented":          "This route's handler has not landed yet",
	"transient-conflict":       "The request kept conflicting with others",
	"dependency-unavailable":   "A dependency is unavailable",
}

// refusal is a request refused with a problem document. Nothing the refused request did inside
// its transaction commits.
type refusal struct {
	status int
	code   string
	detail string
	extra  map[string]any
}

func refuse(status int, code, detail string) *refusal {
	return &refusal{status: status, code: code, detail: detail}
}

func (r *refusal) Error() string { return r.code + ": " + r.detail }

// with adds an extension member (RFC 9457 §3.2).
func (r *refusal) with(name string, v any) *refusal {
	if r.extra == nil {
		r.extra = map[string]any{}
	}
	r.extra[name] = v
	return r
}

// problem answers ref and writes its instance to the server log (§9.4). A 401 and a 403
// identity-revoked carry no epoch headers, whichever check found them: a revocation can be found
// inside the transaction, after the headers were set (§9.1).
func (a *API) problem(w http.ResponseWriter, q *request, ref *refusal) {
	a.o.logf("%s %s %s: %d %s: %s", q.id, q.r.Method, q.r.URL.EscapedPath(), ref.status, ref.code, ref.detail)
	if ref.status == http.StatusUnauthorized || ref.code == "identity-revoked" {
		w.Header().Del("Bronzeward-Epoch")
		w.Header().Del("Bronzeward-Recovery-Mode")
	}
	writeProblem(w, q.id, ref)
}

// writeProblem answers application/problem+json; instance is the request's identifier.
func writeProblem(w http.ResponseWriter, instance string, r *refusal) {
	if r.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}
	writeJSON(w, "application/problem+json", r.status, problemDoc(instance, r))
}

// problemDoc is r's problem document (RFC 9457): a response's, or a failed operation's error.
func problemDoc(instance string, r *refusal) map[string]any {
	doc := map[string]any{
		"type":     "urn:bronzeward:problem:" + r.code,
		"title":    titles[r.code],
		"status":   r.status,
		"instance": instance,
	}
	if r.detail != "" {
		doc["detail"] = r.detail
	}
	for k, v := range r.extra {
		doc[k] = v
	}
	return doc
}

func writeJSON(w http.ResponseWriter, contentType string, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil { // every value this package writes marshals
		panic(err)
	}
	writeBytes(w, contentType, status, b)
}

func writeBytes(w http.ResponseWriter, contentType string, status int, b []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
