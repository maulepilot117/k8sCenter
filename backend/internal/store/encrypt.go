package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// deriveKey derives a 32-byte AES-256 key from the master secret using SHA-256.
func deriveKey(masterSecret string) []byte {
	hash := sha256.Sum256([]byte(masterSecret))
	return hash[:]
}

// Encrypt encrypts plaintext using AES-256-GCM with the given master secret.
// Returns ciphertext with the nonce prepended.
func Encrypt(plaintext []byte, masterSecret string) ([]byte, error) {
	if len(masterSecret) == 0 {
		return nil, fmt.Errorf("encryption key is empty")
	}

	block, err := aes.NewCipher(deriveKey(masterSecret))
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts ciphertext encrypted by Encrypt.
func Decrypt(ciphertext []byte, masterSecret string) ([]byte, error) {
	if len(masterSecret) == 0 {
		return nil, fmt.Errorf("encryption key is empty")
	}

	block, err := aes.NewCipher(deriveKey(masterSecret))
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	return plaintext, nil
}
