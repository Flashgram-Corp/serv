package wallet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrWalletKeyFile marks a missing or malformed wallet master key file. The
// error never carries key material.
var ErrWalletKeyFile = errors.New("wallet master key file is unusable")

// ErrWalletCreationDisabled reports that telesrv was started without a wallet
// master key, so it cannot custody a generated wallet. Callers surface it as an
// unavailable capability, never as a generic failure.
var ErrWalletCreationDisabled = errors.New("server-held wallet creation is disabled")

// KeyCipher seals wallet private keys with a process-wide AES-256-GCM master
// key. The key lives in its own 0600 file next to the wallet state, so backing
// up data/ keeps stored wallets usable; losing it makes them unrecoverable.
type KeyCipher struct {
	aead cipher.AEAD
}

// LoadOrCreateKeyCipher reads the raw 32-byte master key from path, generating
// it on first use. The file must not be group or world accessible.
func LoadOrCreateKeyCipher(path string) (*KeyCipher, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrWalletKeyFile
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		raw = make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return nil, err
		}
		if err = writeKeyFile(path, raw); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(path); statErr != nil {
		return nil, statErr
	} else if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%w: %s must have mode 0600", ErrWalletKeyFile, path)
	}
	return newKeyCipher(decodeKeyFile(path, raw))
}

// decodeKeyFile turns what is on disk back into the 32 raw bytes newKeyCipher
// wants.
//
// writeKeyFile persists the key hex-encoded with a trailing newline, so the file
// is 65 bytes and reading it straight into newKeyCipher made the wallet work on
// first boot -- where the freshly generated in-memory key was still in hand --
// and fail on every boot after it, because 65 != 32. The failure looked like a
// corrupt key file rather than an encoding mismatch, which is what made it worth
// a round-trip test rather than a hand check.
//
// Both encodings are accepted: the hex form this build writes, and the bare 32
// bytes an earlier build could have left behind, so an existing deployment is
// repaired in place instead of needing its key rotated. Rotating would orphan
// every wallet already sealed under the old key.
func decodeKeyFile(path string, raw []byte) []byte {
	trimmed := bytes.TrimRight(raw, "\n")
	if len(trimmed) == hex.EncodedLen(32) {
		if decoded, err := hex.DecodeString(string(trimmed)); err == nil {
			return decoded
		}
	}
	return raw
}

func writeKeyFile(path string, raw []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.WriteString(hex.EncodeToString(raw) + "\n"); err != nil {
		return err
	}
	return f.Sync()
}

func newKeyCipher(raw []byte) (*KeyCipher, error) {
	if len(raw) != 32 {
		return nil, ErrWalletKeyFile
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &KeyCipher{aead: aead}, nil
}

// Seal encrypts wallet private key material. The nonce is stored next to the
// ciphertext; both are base64/hex encoded by the caller's record format.
func (k *KeyCipher) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	if k == nil || k.aead == nil || len(plaintext) == 0 {
		return nil, nil, ErrWalletKeyFile
	}
	nonce = make([]byte, k.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return k.aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

// Open reverses Seal. A wrong master key fails as an authentication error, not
// as garbage plaintext.
func (k *KeyCipher) Open(ciphertext, nonce []byte) ([]byte, error) {
	if k == nil || k.aead == nil || len(nonce) != k.aead.NonceSize() || len(ciphertext) == 0 {
		return nil, ErrWalletKeyFile
	}
	return k.aead.Open(nil, nonce, ciphertext, nil)
}
