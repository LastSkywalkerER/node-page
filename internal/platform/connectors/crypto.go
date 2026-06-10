package connectors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// Cipher encrypts/decrypts connector secrets with AES-256-GCM under a key
// derived from the cluster-shared JWT secret. Every node in a Raft cluster
// shares that secret (replicated via CmdAuthSecretSet), so ciphertext written
// by one node is readable by whichever node currently polls.
type Cipher struct {
	key [32]byte
}

// NewCipher derives the encryption key from the given JWT secret.
func NewCipher(jwtSecret string) *Cipher {
	return &Cipher{key: sha256.Sum256([]byte("node-stats:connectors:v1:" + jwtSecret))}
}

func (c *Cipher) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt returns nonce||ciphertext for the given plaintext secret.
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	aead, err := c.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt reverses Encrypt. A wrong key (JWT secret changed) returns an error.
func (c *Cipher) Decrypt(enc []byte) (string, error) {
	aead, err := c.aead()
	if err != nil {
		return "", err
	}
	if len(enc) < aead.NonceSize() {
		return "", errors.New("connectors: ciphertext too short")
	}
	plain, err := aead.Open(nil, enc[:aead.NonceSize()], enc[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
