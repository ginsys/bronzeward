package provider

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/ginsys/bronzeward/internal/id"
)

// GenerationPath is the KV path of one provider generation, gen/<cluster>/<claim>/<value>
// (compilation.md §2.3 step 6, persistence-api.md §6.4): a cluster identifier, the staging claim's
// identifier and a value identifier the application generates. Only NewGenerationPath makes a
// usable one; the zero value addresses nothing and is refused by CreateGeneration.
type GenerationPath struct{ cluster, claim, value string }

// valueID is the generation column's last component (0004_adoption.sql): it excludes '/', '.',
// '%', '#', '?' and whitespace, so a value identifier is one URL path segment carried literally.
var valueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// NewGenerationPath refuses a cluster that is not a cl_ identifier, a claim that is not an ing_
// identifier, and a value identifier the generation column would refuse.
func NewGenerationPath(cluster, claim, value string) (GenerationPath, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return GenerationPath{}, fmt.Errorf("provider: generation path cluster: %w", err)
	}
	if err := id.MustHave(claim, id.Ingestion); err != nil {
		return GenerationPath{}, fmt.Errorf("provider: generation path claim: %w", err)
	}
	if !valueID.MatchString(value) {
		return GenerationPath{}, errors.New("provider: a generation's value identifier must be 1 to 128 characters of [A-Za-z0-9_-]")
	}
	return GenerationPath{cluster, claim, value}, nil
}

var valueIDEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewValueID returns 128 bits from crypto/rand as 26 characters of lower-case base32, the random
// value identifier of compilation.md §2.3 step 6.
func NewValueID() string {
	var b [16]byte
	rand.Read(b[:]) // never returns an error (crypto/rand, Go 1.24+)
	return valueIDEncoding.EncodeToString(b[:])
}

// String is the path below the KV mount: gen/<cluster>/<claim>/<value>.
func (p GenerationPath) String() string {
	return "gen/" + p.cluster + "/" + p.claim + "/" + p.value
}

func (p GenerationPath) zero() bool { return p == GenerationPath{} }

// kvDataPath is the request path of a generation's KV v2 data.
func kvDataPath(p GenerationPath) string { return "/v1/secret/data/" + p.String() }

// transitPath is the request path of a Transit operation on one named key. The name is written
// into the URL path as it stands, so it must be one segment that a URL carries literally: a '/'
// would address another endpoint ("k/../../sys/…"), '#' and '?' end the path, '%' begins an escape
// the server decodes, and "." or ".." is collapsed (E1's rules). Such a name is refused, never
// escaped.
func transitPath(op, key string) (string, error) {
	if err := keySegment(key); err != nil {
		return "", err
	}
	return "/v1/transit/" + op + "/" + key, nil
}

// CheckKeyName applies transitPath's rule to a configured key name, so configuration is refused at
// load by the same rule NewIngestion applies.
func CheckKeyName(key string) error { return keySegment(key) }

// maxKeyRef is the bound of import_base_revision.baseline_digest_key (0004_adoption.sql), where a
// Digest's KeyRef, "transit/<key>@v<N>", is stored. maxKeyName leaves room for any version Digest
// accepts (strconv.Atoi: at most 19 digits), so a key that NewIngestion and configuration accept
// never yields a reference the column refuses after the provider writes have been made.
const (
	maxKeyRef  = 256
	maxKeyName = maxKeyRef - len("transit/@v") - len("9223372036854775807")
)

func keySegment(key string) error {
	if key == "" || key == "." || key == ".." {
		return fmt.Errorf("provider: transit key name %q is empty, \".\" or \"..\"", key)
	}
	if len(key) > maxKeyName {
		return fmt.Errorf("provider: a transit key name is %d bytes; at most %d fit a %d-byte key reference \"transit/<key>@v<N>\"", len(key), maxKeyName, maxKeyRef)
	}
	if i := strings.IndexFunc(key, func(r rune) bool {
		return r == '/' || r == '#' || r == '?' || r == '%' || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r)
	}); i >= 0 {
		return fmt.Errorf("provider: transit key name %q holds %q, which a URL path would not carry as one literal segment", key, key[i])
	}
	return nil
}
