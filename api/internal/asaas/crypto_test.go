package asaas

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func fixedTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEncryptDecryptAPIKeyRoundTrip(t *testing.T) {
	key := fixedTestKey(t)
	plaintext := "sk_live_asaas_fake_subaccount_key"

	ciphertext, nonce, err := EncryptAPIKey(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(ciphertext) == plaintext {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := DecryptAPIKey(key, ciphertext, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptAPIKeyWrongKeyFails(t *testing.T) {
	key := fixedTestKey(t)
	otherKey := fixedTestKey(t)

	ciphertext, nonce, err := EncryptAPIKey(key, "secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := DecryptAPIKey(otherKey, ciphertext, nonce); err == nil {
		t.Fatal("expected decrypt with wrong key to fail")
	}
}

func TestMasterKeyFromHex(t *testing.T) {
	if _, err := MasterKeyFromHex("not-hex!!"); err == nil {
		t.Fatal("expected invalid hex to error")
	}
	if _, err := MasterKeyFromHex("aabb"); err == nil {
		t.Fatal("expected short key to error")
	}
	valid := make([]byte, MasterKeySize)
	if _, err := rand.Read(valid); err != nil {
		t.Fatal(err)
	}
	hexKey := hex.EncodeToString(valid)
	key, err := MasterKeyFromHex(hexKey)
	if err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if len(key) != MasterKeySize {
		t.Fatalf("got key len %d, want %d", len(key), MasterKeySize)
	}
}
