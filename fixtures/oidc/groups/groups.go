// Package groups is the Actions v2 target the test identity provider calls before it issues an
// access token (mise run dev-idp, fixtures/README.md). It answers with a groups claim holding the
// user's role keys in one project, the group names examples/bronzeward.yaml maps to roles.
// Zitadel's own roles claim is an object, which the server refuses as groups
// (internal/auth/auth.go:209-221), and the pinned release has no groups scope.
package groups

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Skew is how far a request's signed timestamp may be from now.
const Skew = 5 * time.Minute

const function = "function/preaccesstoken"

// request is the part of Zitadel's preaccesstoken call the target reads. user_grants is absent
// for a user with no grant.
type request struct {
	Function   string `json:"function"`
	UserGrants []struct {
		ProjectID string   `json:"project_id"`
		Roles     []string `json:"roles"`
	} `json:"user_grants"`
}

type claim struct {
	Key   string   `json:"key"`
	Value []string `json:"value"`
}

// Handler answers Zitadel's preaccesstoken call for project. Every request must carry a
// Zitadel-Signature made with the target's signing key, which key returns; anything else is
// refused, and the target is created with interruptOnError, so a refusal or an unreachable target
// fails the token request instead of issuing a token without groups. key is called per request:
// Zitadel creates the key with the target, and refuses to create a target whose host does not
// resolve yet, so the target runs before its key exists.
func Handler(key func() ([]byte, error), project string, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		k, err := key()
		if err != nil || len(k) == 0 {
			http.Error(w, "no signing key", http.StatusServiceUnavailable)
			return
		}
		if err := verify(k, r.Header.Get("Zitadel-Signature"), body, now()); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil || req.Function != function {
			http.Error(w, "not a "+function+" call", http.StatusBadRequest)
			return
		}
		groups := []string{} // [] for no grant: the claim is always an array
		for _, g := range req.UserGrants {
			if g.ProjectID == project {
				groups = append(groups, g.Roles...)
			}
		}
		slices.Sort(groups)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]claim{"append_claims": {{Key: "groups", Value: slices.Compact(groups)}}})
	})
}

// verify checks a "t=<unix seconds>,v1=<hex HMAC-SHA256 of t.body>" header; several v1 values
// may be present while the key rotates.
func verify(key []byte, header string, body []byte, now time.Time) error {
	var ts string
	var sigs []string
	for p := range strings.SplitSeq(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return errors.New("no Zitadel-Signature")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > Skew || d < -Skew {
		return errors.New("Zitadel-Signature timestamp outside the allowed skew")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ts + "."))
	m.Write(body)
	want := m.Sum(nil)
	for _, s := range sigs {
		if got, err := hex.DecodeString(s); err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("Zitadel-Signature does not match")
}
