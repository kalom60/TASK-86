package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// EncryptField encrypts plaintext using AES-256-GCM with the supplied 32-byte
// key.  The nonce is prepended to the ciphertext and the whole thing is
// returned as standard base64 so it is safe to store in a TEXT column.
func EncryptField(key []byte, plaintext string) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("crypto: encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: generate nonce: %w", err)
	}

	// Seal appends the ciphertext+tag to nonce.
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptField is the inverse of EncryptField.  It decodes the base64 blob,
// extracts the nonce, and returns the plaintext.
func DecryptField(key []byte, ciphertext string) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("crypto: encryption key must be 32 bytes, got %d", len(key))
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("crypto: base64 decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("crypto: ciphertext too short")
	}

	nonce, data := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}

	return string(plaintext), nil
}

// HashPassword hashes password using bcrypt at cost 12.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("crypto: hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword returns true when password matches the stored bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// MaskName converts a full name into initials with a trailing period on each
// part.  Examples:
//
//	"John Doe"       → "J. D."
//	"Alice"          → "A."
//	"Mary Jane Watson" → "M. J. W."
//
// Empty input is returned unchanged.
func MaskName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	parts := strings.Fields(name)
	var sb strings.Builder
	for i, part := range parts {
		r, size := utf8.DecodeRuneInString(part)
		if size == 0 {
			continue
		}
		_ = r
		// Write just the first Unicode character of each word followed by ".".
		sb.WriteString(string([]rune(part)[:1]))
		sb.WriteByte('.')
		if i < len(parts)-1 {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// MaskID masks all but the last four characters of id with asterisks.
// Examples:
//
//	"1234567890"  → "******7890"
//	"AB12"        → "AB12"   (≤4 chars: returned as-is)
//	""            → ""
func MaskID(id string) string {
	runes := []rune(id)
	n := len(runes)
	if n <= 4 {
		return id
	}
	masked := make([]rune, n)
	for i := 0; i < n-4; i++ {
		masked[i] = '*'
	}
	copy(masked[n-4:], runes[n-4:])
	return string(masked)
}
