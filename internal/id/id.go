// Package id generates and parses the identifiers that leave the database (persistence-api.md
// §2): a prefix naming the entity, then 128 bits from crypto/rand in lower-case, unpadded
// RFC 4648 base32. None comes from a database sequence, so a restore cannot reissue one.
package id

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"slices"
	"strings"
)

type Prefix string

const (
	Cluster            Prefix = "cl"
	Machine            Prefix = "mch"
	Fragment           Prefix = "frg"
	FragmentRevision   Prefix = "frv"
	Profile            Prefix = "prf"
	ProfileRevision    Prefix = "prv"
	Assignment         Prefix = "asg"
	AssignmentRevision Prefix = "asr"
	ImportBase         Prefix = "ibr"
	Draft              Prefix = "drf"
	Release            Prefix = "rel"
	Epoch              Prefix = "ep"
	Request            Prefix = "req"
	Plan               Prefix = "pln"
	Approval           Prefix = "apr"
	Operation          Prefix = "op"
	Attempt            Prefix = "att"
	Observation        Prefix = "obs"
	Principal          Prefix = "idn"
	Token              Prefix = "tok"
	Ingestion          Prefix = "ing"
	Act                Prefix = "act"
	Dependency         Prefix = "dep"
	DependencyAlert    Prefix = "dal"
)

// all is persistence-api.md §2's prefix table. It stays unexported so no caller can change what
// New and Parse accept.
var all = []Prefix{
	Cluster, Machine, Fragment, FragmentRevision, Profile, ProfileRevision, Assignment,
	AssignmentRevision, ImportBase, Draft, Release, Epoch, Request, Plan, Approval, Operation,
	Attempt, Observation, Principal, Token, Ingestion, Act, Dependency, DependencyAlert,
}

// All returns a copy of every prefix of persistence-api.md §2's table.
func All() []Prefix { return slices.Clone(all) }

var enc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

const bodyLen = 26 // ceil(128/5)

// New returns a fresh identifier with prefix p. It panics if p is not in the table: such an
// identifier could be stored but never parsed back, and only a programming error produces one.
func New(p Prefix) string {
	if !known(p) {
		panic(fmt.Sprintf("id.New: unknown prefix %q", p))
	}
	var b [16]byte
	rand.Read(b[:]) // never returns an error (crypto/rand, Go 1.24+)
	return string(p) + "_" + enc.EncodeToString(b[:])
}

// Parse returns the prefix of s, refusing anything New could not have produced.
func Parse(s string) (Prefix, error) {
	p, body, ok := strings.Cut(s, "_")
	if !ok || !known(Prefix(p)) {
		return "", fmt.Errorf("identifier %q: unknown prefix", s)
	}
	if len(body) != bodyLen {
		return "", fmt.Errorf("identifier %q: body is %d characters, want %d", s, len(body), bodyLen)
	}
	// encoding/base32 has no strict mode: re-encoding refuses non-zero trailing bits, which
	// decode to the same 16 bytes as the canonical form.
	b, err := enc.DecodeString(body)
	if err != nil || len(b) != 16 || enc.EncodeToString(b) != body {
		return "", fmt.Errorf("identifier %q: not 128-bit lower-case base32", s)
	}
	return Prefix(p), nil
}

// MustHave refuses s unless it parses with prefix want.
func MustHave(s string, want Prefix) error {
	got, err := Parse(s)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("identifier %q: prefix %q, want %q", s, got, want)
	}
	return nil
}

func known(p Prefix) bool { return slices.Contains(all, p) }
