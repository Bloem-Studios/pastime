// Package sealed encrypts the few values the server must read back: notification
// targets, authenticator secrets and similar. Each value is bound to a purpose, so a
// ciphertext stored for one use cannot be replayed as another.
package sealed

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// ErrNoKey reports that SECRET_KEY is not configured.
var ErrNoKey = errors.New("SECRET_KEY is not configured")

const version = 2

type Box struct {
	aead     cipher.AEAD
	id       [8]byte
	previous []*Box
}

// New parses a base64 32-byte key. An empty key returns a nil Box whose methods fail
// with ErrNoKey, so features that need it can report themselves unavailable.
func New(key string) (*Box, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(key)
	}
	if err != nil || len(raw) != 32 {
		return nil, errors.New("SECRET_KEY must be 32 random bytes, base64-encoded")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	var id [8]byte
	copy(id[:], digest[:8])
	return &Box{aead: aead, id: id}, nil
}

func (b *Box) Available() bool { return b != nil }

func (b *Box) Seal(plaintext []byte, purpose string) ([]byte, error) {
	if b == nil {
		return nil, ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	header := append([]byte{version}, b.id[:]...)
	out := append(header, nonce...)
	return b.aead.Seal(out, nonce, plaintext, append([]byte(purpose), header...)), nil
}

func (b *Box) Open(data []byte, purpose string) ([]byte, error) {
	if b == nil {
		return nil, ErrNoKey
	}
	keys := append([]*Box{b}, b.previous...)
	for _, key := range keys {
		n := key.aead.NonceSize()
		if len(data) > 1+n && data[0] == 1 {
			if plain, err := key.aead.Open(nil, data[1:1+n], data[1+n:], []byte(purpose)); err == nil {
				return plain, nil
			}
		}
		if len(data) > 9+n && data[0] == version && bytes.Equal(data[1:9], key.id[:]) {
			return key.aead.Open(nil, data[9:9+n], data[9+n:], append([]byte(purpose), data[:9]...))
		}
	}
	return nil, errors.New("sealed value cannot be opened with configured keys")
}

// NewKeyring encrypts with the current key and accepts previous keys for decryption.
// Keep previous keys until stored secrets have been rewritten and backup retention expires.
func NewKeyring(current string, previous ...string) (*Box, error) {
	b, err := New(current)
	if err != nil {
		return nil, err
	}
	for _, raw := range previous {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if b == nil {
			return nil, ErrNoKey
		}
		key, err := New(raw)
		if err != nil {
			return nil, err
		}
		b.previous = append(b.previous, key)
	}
	return b, nil
}
