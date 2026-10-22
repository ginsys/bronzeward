package groups

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	key = []byte("synthetic-signing-key")
	now = time.Unix(1_800_000_000, 0)
)

func sign(k []byte, t time.Time, body string) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, k)
	m.Write([]byte(ts + "." + body))
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// call posts body with the signature header sig and returns the status and, on 200, the groups.
func call(t *testing.T, method, sig, body string) (int, []string) {
	t.Helper()
	r := httptest.NewRequest(method, "/groups", strings.NewReader(body))
	if sig != "" {
		r.Header.Set("Zitadel-Signature", sig)
	}
	rec := httptest.NewRecorder()
	Handler(func() ([]byte, error) { return key, nil }, "p1", func() time.Time { return now }).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var out struct {
		AppendClaims []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"append_claims"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.AppendClaims) != 1 || out.AppendClaims[0].Key != "groups" {
		t.Fatalf("answer %s (%v)", rec.Body, err)
	}
	var g []string
	// The raw value, so that null (which the server would accept as absent) is told from [].
	if string(out.AppendClaims[0].Value) == "null" || json.Unmarshal(out.AppendClaims[0].Value, &g) != nil {
		t.Fatalf("groups value %s is not an array of strings", out.AppendClaims[0].Value)
	}
	return rec.Code, g
}

// Before dev-idp has written the key, every call is refused, so no token is issued.
func TestGroupsNoKeyYet(t *testing.T) {
	body := `{"function":"function/preaccesstoken"}`
	r := httptest.NewRequest(http.MethodPost, "/groups", strings.NewReader(body))
	r.Header.Set("Zitadel-Signature", sign(nil, now, body))
	rec := httptest.NewRecorder()
	noKey := func() ([]byte, error) { return nil, nil }
	Handler(noKey, "p1", func() time.Time { return now }).ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestGroups(t *testing.T) {
	grants := `{"function":"function/preaccesstoken","user_grants":[` +
		`{"project_id":"p1","roles":["bw-publishers","bw-authors"]},` +
		`{"project_id":"p2","roles":["bw-recovery"]},` +
		`{"project_id":"p1","roles":["bw-authors"]}]}`
	none := `{"function":"function/preaccesstoken","user":{"id":"u"}}`
	for _, c := range []struct {
		name, method, sig, body string
		code                    int
		groups                  []string
	}{
		{"this project's roles, sorted and unique", http.MethodPost, sign(key, now, grants), grants, 200, []string{"bw-authors", "bw-publishers"}},
		{"no grant is an empty array", http.MethodPost, sign(key, now, none), none, 200, []string{}},
		{"key rotation: a second v1", http.MethodPost, sign([]byte("old"), now, none) + ",v1=" + strings.Split(sign(key, now, none), "v1=")[1], none, 200, []string{}},
		{"skew at the limit", http.MethodPost, sign(key, now.Add(-Skew), none), none, 200, []string{}},
		{"no signature", http.MethodPost, "", none, 401, nil},
		{"other key", http.MethodPost, sign([]byte("other"), now, none), none, 401, nil},
		{"body changed after signing", http.MethodPost, sign(key, now, none), grants, 401, nil},
		{"stale", http.MethodPost, sign(key, now.Add(-Skew-time.Second), none), none, 401, nil},
		{"future", http.MethodPost, sign(key, now.Add(Skew+time.Second), none), none, 401, nil},
		{"v1 not hex", http.MethodPost, "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=zz", none, 401, nil},
		{"another function", http.MethodPost, sign(key, now, `{"function":"function/preuserinfo"}`), `{"function":"function/preuserinfo"}`, 400, nil},
		{"not JSON", http.MethodPost, sign(key, now, "{"), "{", 400, nil},
		{"GET", http.MethodGet, sign(key, now, ""), "", 405, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, g := call(t, c.method, c.sig, c.body)
			if code != c.code || !slices.Equal(g, c.groups) {
				t.Fatalf("got %d %q, want %d %q", code, g, c.code, c.groups)
			}
		})
	}
}
