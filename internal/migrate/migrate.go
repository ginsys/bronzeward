// Package migrate applies the schema migrations embedded in the binary, records the
// installation epoch, and checks at server start that the database holds exactly the binary's
// migrations (persistence-api.md §11, §12.1).
package migrate

import (
	"cmp"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migration is one file migrations/NNNN_<name>.sql. Checksum is the hex SHA-256 of the file,
// recorded when it is applied and compared at every later run and at server start.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// Embedded returns the binary's migrations in version order.
func Embedded() ([]Migration, error) { return load(embedded, "migrations") }

// load refuses anything but NNNN_<name>.sql files numbered 1, 2, 3... without gaps.
func load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		base, isSQL := strings.CutSuffix(name, ".sql")
		num, rest, ok := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if !isSQL || !ok || len(num) != 4 || rest == "" || err != nil || v < 1 {
			return nil, fmt.Errorf("migrations: %s is not NNNN_<name>.sql with NNNN from 0001", name)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		sum := sha256.Sum256(body)
		ms = append(ms, Migration{Version: v, Name: rest, SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("migrations: none in %s", dir)
	}
	slices.SortFunc(ms, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations: version %d where %d was expected; versions run from 1 without gaps or repeats", m.Version, i+1)
		}
	}
	return ms, nil
}
