package baseline

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// input stands in for an observed effective configuration. It carries a recognisable string so a
// test can assert it never reaches an error message or a saved file.
const input = "machine:\n  token: E1-BASELINE-PLAINTEXT-MUST-NOT-APPEAR\n"

// nonceCipher behaves the way transit does in the one respect that matters here: a fresh nonce per
// call, so encrypting identical input twice gives different ciphertext. A stand-in without that
// property would let a byte-for-byte comparison pass and hide the very mistake this package was
// written to correct.
type nonceCipher struct {
	calls      int
	corrupt    bool
	encryptErr error
	decryptErr error
}

func (c *nonceCipher) Encrypt(_ context.Context, keyName string, plaintext []byte) (string, error) {
	if c.encryptErr != nil {
		return "", c.encryptErr
	}
	c.calls++
	return fmt.Sprintf("vault:v1:%s:%d:%s", keyName, c.calls,
		base64.StdEncoding.EncodeToString(plaintext)), nil
}

func (c *nonceCipher) Decrypt(_ context.Context, _ string, ciphertext string) ([]byte, error) {
	if c.decryptErr != nil {
		return nil, c.decryptErr
	}
	parts := strings.Split(ciphertext, ":")
	if len(parts) != 5 {
		return nil, fmt.Errorf("stand-in: malformed ciphertext")
	}
	plaintext, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, err
	}
	if c.corrupt {
		return append(plaintext, '!'), nil
	}
	return plaintext, nil
}

// TestMakeRecordsTheInputAndProvesItDecrypts covers the honest path, including the round trip Make
// performs before returning.
func TestMakeRecordsTheInputAndProvesItDecrypts(t *testing.T) {
	c := &nonceCipher{}

	b, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	sum := sha256.Sum256([]byte(input))
	if want := hex.EncodeToString(sum[:]); b.InputSHA256 != want {
		t.Errorf("InputSHA256 = %q, want %q", b.InputSHA256, want)
	}
	if b.InputBytes != len(input) {
		t.Errorf("InputBytes = %d, want %d", b.InputBytes, len(input))
	}
	if b.RunID != "run-1" || b.KeyName != "bw-artifact" {
		t.Errorf("run id %q, key %q", b.RunID, b.KeyName)
	}
	if b.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if err := b.Verify(t.Context(), c); err != nil {
		t.Errorf("Verify on a freshly made baseline: %v", err)
	}
}

// TestTwoRunsOverIdenticalInputDifferInCiphertextAndAgreeOnTheDigest is the corrected acceptance
// criterion, demonstrated rather than asserted in prose.
//
// Issue 2 originally asked for the encrypted baseline to be compared byte for byte across runs.
// Transit's per-call nonce makes that comparison fail for a correct implementation. What
// establishes that the same configuration was retained is the input digests agreeing, and this
// test fails if either half of that stops being true.
func TestTwoRunsOverIdenticalInputDifferInCiphertextAndAgreeOnTheDigest(t *testing.T) {
	c := &nonceCipher{}

	first, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err != nil {
		t.Fatalf("Make (first): %v", err)
	}
	second, err := Make(t.Context(), c, "bw-artifact", "run-2", []byte(input))
	if err != nil {
		t.Fatalf("Make (second): %v", err)
	}

	if first.Ciphertext == second.Ciphertext {
		t.Error("two runs over identical input produced identical ciphertext; the stand-in is not " +
			"behaving like transit, so this test proves nothing about the criterion")
	}
	if first.InputSHA256 != second.InputSHA256 {
		t.Error("two runs over identical input recorded different input digests")
	}
	if !SameInput(first, second) {
		t.Error("SameInput says two baselines of identical input are different")
	}

	// And the other direction: different input must not be reported as the same.
	third, err := Make(t.Context(), c, "bw-artifact", "run-3", []byte(input+"extra: value\n"))
	if err != nil {
		t.Fatalf("Make (third): %v", err)
	}
	if SameInput(first, third) {
		t.Error("SameInput says two baselines of different input are the same")
	}
}

// TestMakeFailsClosedWhenTheRoundTripDoesNot is the property that makes this a baseline rather
// than a record that one was kept. A configuration that cannot be decrypted back is gone, and
// returning a Baseline for it would be worse than returning an error: the evidence would say the
// configuration was retained.
func TestMakeFailsClosedWhenTheRoundTripDoesNot(t *testing.T) {
	c := &nonceCipher{corrupt: true}

	b, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err == nil {
		t.Fatal("Make returned a baseline that does not decrypt back to its input")
	}
	if b.Ciphertext != "" {
		t.Error("Make returned a usable baseline after failing")
	}
	if !strings.Contains(err.Error(), "run-1") {
		t.Errorf("the error does not name the run: %v", err)
	}
}

// TestMakeReportsProviderFailures checks each side of the round trip surfaces its own error rather
// than being reported as a digest mismatch, which would send a reader looking in the wrong place.
func TestMakeReportsProviderFailures(t *testing.T) {
	encryptFailed := &nonceCipher{encryptErr: errors.New("transit sealed")}
	if _, err := Make(t.Context(), encryptFailed, "bw-artifact", "run-1", []byte(input)); err == nil {
		t.Error("Make succeeded despite an encryption failure")
	} else if !strings.Contains(err.Error(), "transit sealed") {
		t.Errorf("the encryption failure was not reported: %v", err)
	}

	decryptFailed := &nonceCipher{decryptErr: errors.New("transit sealed")}
	if _, err := Make(t.Context(), decryptFailed, "bw-artifact", "run-1", []byte(input)); err == nil {
		t.Error("Make succeeded despite a decryption failure")
	} else if !strings.Contains(err.Error(), "transit sealed") {
		t.Errorf("the decryption failure was not reported: %v", err)
	}
}

// TestVerifyCatchesATamperedRecord covers the check done by a later, separate process against a
// baseline read off disk, which is where a substituted digest or length would show up.
func TestVerifyCatchesATamperedRecord(t *testing.T) {
	c := &nonceCipher{}
	good, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	wrongDigest := good
	wrongDigest.InputSHA256 = strings.Repeat("0", 64)
	if err := Verify(t, wrongDigest, c); err == nil {
		t.Error("Verify accepted a baseline whose recorded digest does not match")
	}

	wrongLength := good
	wrongLength.InputBytes = good.InputBytes + 1
	if err := Verify(t, wrongLength, c); err == nil {
		t.Error("Verify accepted a baseline whose recorded length does not match")
	}
}

// Verify is a helper so the two cases above read as one line each.
func Verify(t *testing.T, b Baseline, c Cipher) error {
	t.Helper()
	return b.Verify(t.Context(), c)
}

// TestVerifyRefusesAnIncompleteBaseline checks a record missing either half is reported as
// incomplete rather than passing or panicking. A baseline with no ciphertext and a digest would
// otherwise look verifiable until something tried to decrypt nothing.
func TestVerifyRefusesAnIncompleteBaseline(t *testing.T) {
	c := &nonceCipher{}
	for name, b := range map[string]Baseline{
		"empty":            {},
		"no ciphertext":    {InputSHA256: strings.Repeat("a", 64)},
		"no input digest":  {Ciphertext: "vault:v1:x:1:eA=="},
		"neither, with id": {RunID: "run-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := b.Verify(t.Context(), c); err == nil {
				t.Fatal("Verify accepted an incomplete baseline")
			}
		})
	}
}

// TestMakeRejectsAnEmptyInput checks the degenerate case, which would verify perfectly and retain
// nothing.
func TestMakeRejectsAnEmptyInput(t *testing.T) {
	c := &nonceCipher{}
	if _, err := Make(t.Context(), c, "bw-artifact", "run-1", nil); err == nil {
		t.Error("Make accepted an empty input")
	}
	if _, err := Make(t.Context(), c, "bw-artifact", "", []byte(input)); err == nil {
		t.Error("Make accepted an empty run id")
	}
	if _, err := Make(t.Context(), c, "", "run-1", []byte(input)); err == nil {
		t.Error("Make accepted an empty key name")
	}
	if _, err := Make(t.Context(), nil, "bw-artifact", "run-1", []byte(input)); err == nil {
		t.Error("Make accepted a nil cipher")
	}
}

// TestSaveAndLoadRoundTrip covers the on-disk form, which ends up in an evidence bundle and is
// read back by a process that is not the one that wrote it.
func TestSaveAndLoadRoundTrip(t *testing.T) {
	c := &nonceCipher{}
	made, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	path := filepath.Join(t.TempDir(), "nested", "baseline.json")
	if err := Save(path, made); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.InputSHA256 != made.InputSHA256 || loaded.Ciphertext != made.Ciphertext ||
		loaded.InputBytes != made.InputBytes || loaded.RunID != made.RunID || loaded.KeyName != made.KeyName {
		t.Errorf("the baseline did not survive the round trip:\nsaved  %+v\nloaded %+v", made, loaded)
	}
	if err := loaded.Verify(t.Context(), c); err != nil {
		t.Errorf("a baseline read back off disk does not verify: %v", err)
	}
}

// TestTheSavedFileHoldsNoPlaintext checks the file that ends up in an evidence bundle. The
// ciphertext is in it by design; the configuration must not be.
func TestTheSavedFileHoldsNoPlaintext(t *testing.T) {
	c := &nonceCipher{}
	made, err := Make(t.Context(), c, "bw-artifact", "run-1", []byte(input))
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := Save(path, made); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the saved baseline: %v", err)
	}

	if strings.Contains(string(body), "E1-BASELINE-PLAINTEXT-MUST-NOT-APPEAR") {
		t.Errorf("the saved baseline holds the configuration in the clear:\n%s", body)
	}
	// The control for that assertion: this stand-in's ciphertext is base64, so the plaintext is
	// present in encoded form. It confirms the file really does carry the configuration, which is
	// what makes the plain-text absence above meaningful rather than vacuous.
	if !strings.Contains(string(body), base64.StdEncoding.EncodeToString([]byte(input))) {
		t.Errorf("the saved baseline does not carry the configuration at all:\n%s", body)
	}
}

// TestLoadReportsAMalformedFile checks a corrupt baseline fails loudly rather than verifying as an
// empty record.
func TestLoadReportsAMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a malformed file")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("Load accepted a path that does not exist")
	}
}
