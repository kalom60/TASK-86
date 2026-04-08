package unit_tests

import (
	"encoding/base64"
	"strings"
	"testing"

	"w2t86/internal/crypto"
)

// testKey is a 32-byte AES key used across encrypt/decrypt tests.
var testKey = []byte("01234567890123456789012345678901")

// ---------------------------------------------------------------------------
// HashPassword / CheckPassword
// ---------------------------------------------------------------------------

func TestCrypto_HashPassword_ProducesValidHash(t *testing.T) {
	hash, err := crypto.HashPassword("mysecretpassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}
	// bcrypt hashes start with "$2a$" or "$2b$".
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("expected bcrypt hash, got: %s", hash)
	}
}

func TestCrypto_CheckPassword_Correct_ReturnsTrue(t *testing.T) {
	const pw = "correctpassword"
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !crypto.CheckPassword(hash, pw) {
		t.Error("CheckPassword returned false for correct password")
	}
}

func TestCrypto_CheckPassword_Wrong_ReturnsFalse(t *testing.T) {
	hash, err := crypto.HashPassword("correctpassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if crypto.CheckPassword(hash, "wrongpassword") {
		t.Error("CheckPassword returned true for wrong password")
	}
}

func TestCrypto_CheckPassword_EmptyPassword_ReturnsFalse(t *testing.T) {
	hash, err := crypto.HashPassword("somepassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if crypto.CheckPassword(hash, "") {
		t.Error("CheckPassword returned true for empty password")
	}
}

// ---------------------------------------------------------------------------
// EncryptField / DecryptField
// ---------------------------------------------------------------------------

func TestCrypto_EncryptDecrypt_RoundTrip(t *testing.T) {
	const plaintext = "hello, world"
	ciphertext, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	got, err := crypto.DecryptField(testKey, ciphertext)
	if err != nil {
		t.Fatalf("DecryptField: %v", err)
	}
	if got != plaintext {
		t.Errorf("round-trip mismatch: expected %q, got %q", plaintext, got)
	}
}

func TestCrypto_Encrypt_DifferentCiphertextEachTime(t *testing.T) {
	const plaintext = "same plaintext"
	ct1, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField (1): %v", err)
	}
	ct2, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField (2): %v", err)
	}
	if ct1 == ct2 {
		t.Error("expected different ciphertexts for the same plaintext (nonce randomness), got identical")
	}
}

func TestCrypto_Decrypt_TamperedCiphertext_ReturnsError(t *testing.T) {
	ciphertext, err := crypto.EncryptField(testKey, "original")
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}

	// Decode, flip a byte in the middle, re-encode.
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	mid := len(data) / 2
	data[mid] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(data)

	_, err = crypto.DecryptField(testKey, tampered)
	if err == nil {
		t.Error("expected error when decrypting tampered ciphertext, got nil")
	}
}

func TestCrypto_Decrypt_InvalidBase64_ReturnsError(t *testing.T) {
	_, err := crypto.DecryptField(testKey, "not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64 ciphertext, got nil")
	}
}

func TestCrypto_Decrypt_EmptyCiphertext_ReturnsError(t *testing.T) {
	_, err := crypto.DecryptField(testKey, "")
	if err == nil {
		t.Error("expected error for empty ciphertext, got nil")
	}
}

// ---------------------------------------------------------------------------
// MaskName
// ---------------------------------------------------------------------------

func TestCrypto_MaskName_TwoWords(t *testing.T) {
	got := crypto.MaskName("John Doe")
	want := "J. D."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "John Doe", got, want)
	}
}

func TestCrypto_MaskName_SingleWord(t *testing.T) {
	got := crypto.MaskName("Alice")
	want := "A."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "Alice", got, want)
	}
}

func TestCrypto_MaskName_ThreeWords(t *testing.T) {
	got := crypto.MaskName("Mary Jane Watson")
	want := "M. J. W."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "Mary Jane Watson", got, want)
	}
}

func TestCrypto_MaskName_EmptyString(t *testing.T) {
	got := crypto.MaskName("")
	if got != "" {
		t.Errorf("MaskName(%q) = %q, want empty string", "", got)
	}
}

// ---------------------------------------------------------------------------
// MaskID
// ---------------------------------------------------------------------------

func TestCrypto_MaskID_LongID(t *testing.T) {
	got := crypto.MaskID("123456789")
	want := "*****6789"
	if got != want {
		t.Errorf("MaskID(%q) = %q, want %q", "123456789", got, want)
	}
}

func TestCrypto_MaskID_ExactlyFourChars(t *testing.T) {
	got := crypto.MaskID("1234")
	want := "1234"
	if got != want {
		t.Errorf("MaskID(%q) = %q, want %q (≤4 chars unchanged)", "1234", got, want)
	}
}

func TestCrypto_MaskID_ShortID(t *testing.T) {
	got := crypto.MaskID("12")
	want := "12"
	if got != want {
		t.Errorf("MaskID(%q) = %q, want %q", "12", got, want)
	}
}

func TestCrypto_MaskID_Empty(t *testing.T) {
	got := crypto.MaskID("")
	if got != "" {
		t.Errorf("MaskID(%q) = %q, want empty string", "", got)
	}
}
