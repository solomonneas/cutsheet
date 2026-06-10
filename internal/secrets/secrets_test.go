package secrets

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) [32]byte {
	t.Helper()
	var key [32]byte
	copy(key[:], "0123456789abcdef0123456789abcdef")
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	box := New(testKey(t))
	tests := []struct {
		name      string
		plaintext string
	}{
		{"password", "hunter2"},
		{"empty", ""},
		{"binary-ish", "line1\nline2\x00tail"},
		{"long", strings.Repeat("private key material ", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := box.Encrypt([]byte(tt.plaintext))
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if !strings.HasPrefix(enc, "enc:v1:") {
				t.Fatalf("Encrypt: got %q, want enc:v1: prefix", enc)
			}
			if !IsEncrypted(enc) {
				t.Fatalf("IsEncrypted(%q) = false, want true", enc)
			}
			got, err := box.Decrypt(enc)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != tt.plaintext {
				t.Fatalf("round trip: got %q, want %q", got, tt.plaintext)
			}
		})
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	box := New(testKey(t))
	a, err := box.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := box.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if a == b {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext (nonce reuse?)")
	}
}

func TestDecryptRejects(t *testing.T) {
	box := New(testKey(t))
	enc, err := box.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	var otherKey [32]byte
	copy(otherKey[:], "ffffffffffffffffffffffffffffffff")
	other := New(otherKey)

	tampered := enc[:len(enc)-2] + "AA"

	tests := []struct {
		name  string
		box   *Box
		input string
	}{
		{"wrong key", other, enc},
		{"tampered ciphertext", box, tampered},
		{"missing prefix", box, "not-encrypted"},
		{"bad base64", box, "enc:v1:!!!not-base64!!!"},
		{"too short", box, "enc:v1:AAAA"},
		{"wrong version", box, "enc:v2:AAAA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.box.Decrypt(tt.input); err == nil {
				t.Fatal("Decrypt: want error, got nil")
			}
		})
	}
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"enc:v1:abc", true},
		{"enc:v2:abc", false},
		{"plaintext", false},
		{"", false},
		{"enc:v1", false},
	}
	for _, tt := range tests {
		if got := IsEncrypted(tt.input); got != tt.want {
			t.Errorf("IsEncrypted(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOpenFromEnv(t *testing.T) {
	key := testKey(t)
	t.Setenv(EnvKey, hex.EncodeToString(key[:]))

	box, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	enc, err := New(key).Encrypt([]byte("via env"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := box.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "via env" {
		t.Fatalf("Decrypt: got %q", got)
	}
}

func TestOpenFromEnvInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"not hex", strings.Repeat("z", 64)},
		{"too short", "abcd"},
		{"too long", strings.Repeat("ab", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvKey, tt.value)
			if _, err := Open(t.TempDir()); err == nil {
				t.Fatal("Open: want error, got nil")
			}
		})
	}
}

func TestOpenGeneratesAndReusesKeyFile(t *testing.T) {
	t.Setenv(EnvKey, "") // empty counts as unset
	dir := t.TempDir()

	box1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (generate): %v", err)
	}
	keyPath := filepath.Join(dir, "secret.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file perms: got %o, want 600", perm)
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if decoded, err := hex.DecodeString(strings.TrimSpace(string(raw))); err != nil || len(decoded) != 32 {
		t.Fatalf("key file content: want 64 hex chars, got %q (err %v)", raw, err)
	}

	enc, err := box1.Encrypt([]byte("persisted"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	box2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (reload): %v", err)
	}
	got, err := box2.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt after reload: %v", err)
	}
	if string(got) != "persisted" {
		t.Fatalf("Decrypt after reload: got %q", got)
	}
}

func TestOpenRejectsCorruptKeyFile(t *testing.T) {
	t.Setenv(EnvKey, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("write corrupt key: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open with corrupt key file: want error, got nil")
	}
}

func TestOpenCreatesDataDir(t *testing.T) {
	t.Setenv(EnvKey, "")
	dir := filepath.Join(t.TempDir(), "nested", "data")
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open with missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.key")); err != nil {
		t.Fatalf("key file not created: %v", err)
	}
}
