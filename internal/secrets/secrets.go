// Package secrets encrypts collector credentials at rest with NaCl secretbox.
// Encrypted values are self-describing strings ("enc:v1:<base64>") so they can
// live inside collector config JSON in the device registry.
//
// Key resolution order: the CUTSHEET_SECRET_KEY environment variable (64 hex
// chars) wins; otherwise a key is auto-generated at <data-dir>/secret.key with
// 0600 permissions on first use and loaded thereafter. The auto-generated file
// trades key/data separation for a zero-setup default: anyone who can read the
// whole data dir gets the key, but a setup with no env-var step still gets
// real encryption against partial leaks (backups of the database alone, SQL
// access, copied registry dumps).
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/nacl/secretbox"
)

// EnvKey names the environment variable holding the hex-encoded 32-byte key.
const EnvKey = "CUTSHEET_SECRET_KEY"

// keyFileName is the auto-generated key file inside the data dir.
const keyFileName = "secret.key"

// prefix marks encrypted values; the v1 suffix leaves room for future formats.
const prefix = "enc:v1:"

// Box encrypts and decrypts secrets with a fixed 32-byte key.
type Box struct {
	key [32]byte
}

// New builds a Box from a raw 32-byte key.
func New(key [32]byte) *Box {
	return &Box{key: key}
}

// Open resolves the secret key and returns a ready Box. CUTSHEET_SECRET_KEY
// (64 hex chars) takes precedence; otherwise the key at <dataDir>/secret.key
// is loaded, generated first if absent.
func Open(dataDir string) (*Box, error) {
	if env := os.Getenv(EnvKey); env != "" {
		key, err := parseHexKey(env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvKey, err)
		}
		return New(key), nil
	}

	path := filepath.Join(dataDir, keyFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return generateKeyFile(dataDir, path)
	}
	if err != nil {
		return nil, fmt.Errorf("read secret key file: %w", err)
	}
	key, err := parseHexKey(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("secret key file %s: %w", path, err)
	}
	return New(key), nil
}

func generateKeyFile(dataDir, path string) (*Box, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir for secret key: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write secret key file: %w", err)
	}
	return New(key), nil
}

func parseHexKey(s string) ([32]byte, error) {
	var key [32]byte
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return key, fmt.Errorf("invalid key: %w", err)
	}
	if len(decoded) != len(key) {
		return key, fmt.Errorf("invalid key: got %d bytes, want %d (64 hex chars)", len(decoded), len(key))
	}
	copy(key[:], decoded)
	return key, nil
}

// Encrypt seals plaintext and returns an "enc:v1:<base64 nonce+box>" string.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := secretbox.Seal(nonce[:], plaintext, &nonce, &b.key)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens an "enc:v1:..." string produced by Encrypt.
func (b *Box) Decrypt(s string) ([]byte, error) {
	if !IsEncrypted(s) {
		return nil, fmt.Errorf("value is not a %q-prefixed encrypted secret", prefix)
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil {
		return nil, fmt.Errorf("decode secret: %w", err)
	}
	if len(sealed) < 24+secretbox.Overhead {
		return nil, errors.New("decode secret: truncated ciphertext")
	}
	var nonce [24]byte
	copy(nonce[:], sealed[:24])
	plaintext, ok := secretbox.Open(nil, sealed[24:], &nonce, &b.key)
	if !ok {
		return nil, errors.New("decrypt secret: wrong key or corrupted ciphertext")
	}
	return plaintext, nil
}

// IsEncrypted reports whether s carries the enc:v1: secret format.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, prefix)
}
