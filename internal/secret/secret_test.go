package secret

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Keys are sealed with AES-GCM: they come back unchanged, each seal looks
// different, a changed or foreign value is refused, and the key file is kept.
func TestBox(t *testing.T) {
	dir := t.TempDir()
	box, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("sonarr-api-key-123")
	a, _ := box.Seal(plain)
	b, _ := box.Seal(plain)
	if a == b {
		t.Fatal("the same value sealed twice looks the same (nonce reused)")
	}
	if bytes.Contains([]byte(a), plain) {
		t.Fatal("the sealed value contains the key")
	}
	if got, err := box.Open(a); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
	if got, err := box.Open(""); got != nil || err != nil {
		t.Fatalf("empty: %q %v", got, err)
	}

	// A changed byte, a cut value or garbage is refused, not decrypted wrongly.
	raw, _ := base64.StdEncoding.DecodeString(a)
	raw[len(raw)-1] ^= 1
	if _, err := box.Open(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("a changed value was accepted")
	}
	if _, err := box.Open(base64.StdEncoding.EncodeToString(raw[:5])); err == nil {
		t.Fatal("a cut value was accepted")
	}
	if _, err := box.Open("not base64!"); err == nil {
		t.Fatal("garbage was accepted")
	}

	// The key file stays: a restart opens what was sealed before.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.Open(a); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("after a restart: %q %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "secret.key")); fi.Mode().Perm() != 0o600 {
			t.Fatalf("key file readable by others: %v", fi.Mode())
		}
	}

	// Another installation's key cannot open it.
	other, _ := Open(t.TempDir())
	if _, err := other.Open(a); err == nil {
		t.Fatal("a foreign key opened the value")
	}

	// A damaged key file is an error, not a new key that loses everything.
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "secret.key"), []byte("short"), 0o600)
	if _, err := Open(bad); err == nil {
		t.Fatal("a key of the wrong length was accepted")
	}
}
