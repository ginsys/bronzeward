package compile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/contract"
)

// The web console reads the Talos configuration schema of a cluster's contract from a file of its
// own build (docs/spec/configuration-editing.md §3). These tests tie those files to the compiler:
// the asset for the pinned machinery's minor is the module's own file, byte for byte, and the
// contracts that have an asset are the contracts the compiler renders.
const (
	webSchemaDir = "../../web/src/talos-schema"
	moduleSchema = "config/schemas/config.schema.json"
)

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// schemaAssetProblems lists what is wrong with the assets in assetDir, one line each, in order:
//   - a file that is not named <contract>.json;
//   - a supported contract with no asset, and an asset for a contract not on the list;
//   - an asset that does not parse or whose $id does not name its own contract;
//   - for the contract of the pinned module's minor only, an asset that differs from the module's file.
//
// A listed contract older than the pinned minor can be checked for existence and $id only: the
// pinned module holds no schema of that minor.
func schemaAssetProblems(assetDir, moduleDir, pinned string, supported []string) []string {
	var out []string
	entries, err := os.ReadDir(assetDir)
	if err != nil {
		return []string{fmt.Sprintf("asset directory: %v", err)}
	}
	have := map[string]bool{}
	for _, e := range entries {
		c, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok || c == "" {
			out = append(out, fmt.Sprintf("%s is not a <contract>.json asset", e.Name()))
			continue
		}
		have[c] = true
	}
	for _, c := range supported {
		if !have[c] {
			out = append(out, fmt.Sprintf("supported contract %s has no asset %s.json", c, c))
		}
	}
	contracts := make([]string, 0, len(have))
	for c := range have {
		contracts = append(contracts, c)
	}
	sort.Strings(contracts)
	minor, ok := contract.Minor(pinned)
	if !ok {
		// Without a minor no asset is compared with the module's file, so say so.
		out = append(out, fmt.Sprintf("the pinned module version %q has no readable minor, so no asset is compared with the module's file", pinned))
	}
	for _, c := range contracts {
		if !slices.Contains(supported, c) {
			out = append(out, fmt.Sprintf("asset %s.json names a contract that is not on the supported list", c))
		}
		b, err := os.ReadFile(filepath.Join(assetDir, c+".json"))
		if err != nil {
			out = append(out, fmt.Sprintf("asset %s.json: %v", c, err))
			continue
		}
		var doc struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			out = append(out, fmt.Sprintf("asset %s.json does not parse: %v", c, err))
			continue
		}
		if !strings.Contains(doc.ID, "/"+c+"/") {
			out = append(out, fmt.Sprintf("asset %s.json has $id %q, which does not name contract %s", c, doc.ID, c))
		}
		if c != minor {
			continue
		}
		want, err := os.ReadFile(filepath.Join(moduleDir, moduleSchema))
		if err != nil {
			out = append(out, fmt.Sprintf("the pinned module's %s: %v", moduleSchema, err))
			continue
		}
		if !bytes.Equal(b, want) {
			out = append(out, fmt.Sprintf("asset %s.json (sha256 %s) is not the pinned module %s's %s (sha256 %s): run `mise run web-schema` and commit the result",
				c, digest(b), pinned, moduleSchema, digest(want)))
		}
	}
	return out
}

// The machinery module's directory in the version go.mod pins.
func machineryDir(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", machineryModule)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v", machineryModule, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("go list -m %s: no module directory", machineryModule)
	}
	return dir
}

// The web build's schema assets are the pinned module's file for the pinned minor, and exist for
// exactly the contracts the compiler renders. A bump of the pinned module that changes the file
// fails here until the asset is made again.
func TestWebSchemaAssets(t *testing.T) {
	version, _, err := Machinery()
	if err != nil {
		t.Fatal(err)
	}
	supported, err := SupportedContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(supported) == 0 {
		t.Fatal("no supported contract: the check would pass on nothing")
	}
	for _, p := range schemaAssetProblems(webSchemaDir, machineryDir(t), version, supported) {
		t.Error(p)
	}
}

// The check itself: each defect it names is planted in a copy and found, and the clean copy and a
// contract older than the pin pass.
func TestSchemaAssetProblems(t *testing.T) {
	const (
		file114 = `{"$id":"https://talos.dev/v1.14/schemas/config.schema.json","oneOf":[]}`
		file115 = `{"$id":"https://talos.dev/v1.15/schemas/config.schema.json","oneOf":[]}`
	)
	tree := func(t *testing.T, module string, assets map[string]string) (assetDir, moduleDir string) {
		t.Helper()
		assetDir, moduleDir = t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(moduleDir, filepath.Dir(moduleSchema)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moduleDir, moduleSchema), []byte(module), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, body := range assets {
			if err := os.WriteFile(filepath.Join(assetDir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return assetDir, moduleDir
	}
	for _, c := range []struct {
		name      string
		module    string
		assets    map[string]string
		pinned    string
		supported []string
		want      string // a fragment of a problem expected; empty: no problem at all
	}{
		{"clean", file114, map[string]string{"v1.14.json": file114}, "v1.14.5", []string{"v1.14"}, ""},
		{"one byte changed", file114, map[string]string{"v1.14.json": file114 + "\n"}, "v1.14.5", []string{"v1.14"}, "is not the pinned module v1.14.5"},
		{"no asset for a supported contract", file114, map[string]string{}, "v1.14.5", []string{"v1.14"}, "supported contract v1.14 has no asset"},
		{"asset for an unlisted contract", file114, map[string]string{"v1.14.json": file114, "v1.13.json": strings.ReplaceAll(file114, "v1.14", "v1.13")}, "v1.14.5", []string{"v1.14"}, "v1.13.json names a contract that is not on the supported list"},
		{"asset renamed", file114, map[string]string{"v1.15.json": file114}, "v1.14.5", []string{"v1.14"}, "supported contract v1.14 has no asset"},
		{"id names another contract", file115, map[string]string{"v1.14.json": file115}, "v1.14.5", []string{"v1.14"}, `does not name contract v1.14`},
		{"not json", file114, map[string]string{"v1.14.json": "{"}, "v1.14.5", []string{"v1.14"}, "does not parse"},
		{"stray file", file114, map[string]string{"v1.14.json": file114, "notes.txt": "x"}, "v1.14.5", []string{"v1.14"}, "notes.txt is not a <contract>.json asset"},
		// A pin whose minor cannot be read must not skip the digest check without a word.
		{"pin with no readable minor", file114, map[string]string{"v1.14.json": file114 + "\n"}, "not-a-version", []string{"v1.14"}, "has no readable minor"},
		// The pin moved on to v1.15: the module holds no v1.14 file, so the v1.14 asset is checked
		// for existence and $id only.
		{"older contract, pin moved on", file115, map[string]string{"v1.14.json": file114}, "v1.15.0", []string{"v1.14"}, ""},
		{"older contract, wrong id", file115, map[string]string{"v1.14.json": file115}, "v1.15.0", []string{"v1.14"}, `does not name contract v1.14`},
	} {
		t.Run(c.name, func(t *testing.T) {
			assetDir, moduleDir := tree(t, c.module, c.assets)
			got := schemaAssetProblems(assetDir, moduleDir, c.pinned, c.supported)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("problems %q, want none", got)
				}
				return
			}
			if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, c.want) }) {
				t.Fatalf("problems %q, want one containing %q", got, c.want)
			}
		})
	}
	if got := schemaAssetProblems(filepath.Join(t.TempDir(), "absent"), t.TempDir(), "v1.14.5", []string{"v1.14"}); len(got) != 1 || !strings.Contains(got[0], "asset directory") {
		t.Fatalf("a missing directory gave %q", got)
	}
}
