package crypto_test

import (
	"strings"
	"testing"

	"w2t86/internal/crypto"
)

// ---------------------------------------------------------------
// Password hashing
// ---------------------------------------------------------------

func TestHashPassword_ProducesValidBcryptHash(t *testing.T) {
	hash, err := crypto.HashPassword("correcthorsebatterystaple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") {
		t.Errorf("expected bcrypt hash prefix $2a$, got %q", hash[:4])
	}
}

func TestCheckPassword_CorrectPassword(t *testing.T) {
	const pw = "correcthorsebatterystaple"
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !crypto.CheckPassword(hash, pw) {
		t.Error("CheckPassword: expected true for matching password")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	hash, err := crypto.HashPassword("correcthorsebatterystaple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if crypto.CheckPassword(hash, "wrongpassword") {
		t.Error("CheckPassword: expected false for non-matching password")
	}
}

// ---------------------------------------------------------------
// AES-256-GCM encrypt / decrypt
// ---------------------------------------------------------------

var testKey = []byte("12345678901234567890123456789012") // exactly 32 bytes

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	const plaintext = "hello, world!"
	ct, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	got, err := crypto.DecryptField(testKey, ct)
	if err != nil {
		t.Fatalf("DecryptField: %v", err)
	}
	if got != plaintext {
		t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptField_DifferentNonceEachTime(t *testing.T) {
	const plaintext = "same input"
	ct1, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField first: %v", err)
	}
	ct2, err := crypto.EncryptField(testKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptField second: %v", err)
	}
	if ct1 == ct2 {
		t.Error("expected two encryptions of the same plaintext to differ (different nonces)")
	}
}

func TestDecryptField_InvalidBase64_ReturnsError(t *testing.T) {
	_, err := crypto.DecryptField(testKey, "not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64 input")
	}
}

func TestDecryptField_TamperedCiphertext_ReturnsError(t *testing.T) {
	ct, err := crypto.EncryptField(testKey, "secret")
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	// Flip the last character to simulate tampering.
	tampered := ct[:len(ct)-1] + "X"
	if ct[len(ct)-1] == 'X' {
		tampered = ct[:len(ct)-1] + "Y"
	}
	_, err = crypto.DecryptField(testKey, tampered)
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}

// ---------------------------------------------------------------
// MaskName
// ---------------------------------------------------------------

func TestMaskName_TwoWords(t *testing.T) {
	got := crypto.MaskName("John Doe")
	want := "J. D."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "John Doe", got, want)
	}
}

func TestMaskName_SingleWord(t *testing.T) {
	got := crypto.MaskName("Alice")
	want := "A."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "Alice", got, want)
	}
}

func TestMaskName_MultiWord(t *testing.T) {
	got := crypto.MaskName("Mary Jane Watson")
	want := "M. J. W."
	if got != want {
		t.Errorf("MaskName(%q) = %q, want %q", "Mary Jane Watson", got, want)
	}
}

func TestMaskName_Empty(t *testing.T) {
	got := crypto.MaskName("")
	if got != "" {
		t.Errorf("MaskName(%q) = %q, want empty string", "", got)
	}
}

// ---------------------------------------------------------------
// MaskID
// ---------------------------------------------------------------

func TestMaskID_Long(t *testing.T) {
	got := crypto.MaskID("123456789")
	want := "*****6789"
	if got != want {
		t.Errorf("MaskID(%q) = %q, want %q", "123456789", got, want)
	}
}

func TestMaskID_Short(t *testing.T) {
	// 4 or fewer characters — returned as-is
	got := crypto.MaskID("123")
	if got != "123" {
		t.Errorf("MaskID(%q) = %q, want %q", "123", got, "123")
	}
	got4 := crypto.MaskID("AB12")
	if got4 != "AB12" {
		t.Errorf("MaskID(%q) = %q, want %q", "AB12", got4, "AB12")
	}
}

func TestMaskID_Empty(t *testing.T) {
	got := crypto.MaskID("")
	if got != "" {
		t.Errorf("MaskID(%q) = %q, want empty string", "", got)
	}
}
