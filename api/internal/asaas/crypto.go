package asaas

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// MasterKeySize is the required AES-256 key length in bytes.
const MasterKeySize = 32

// MasterKeyFromHex decodes the SSM SecureString value (stored as hex) into a
// usable AES-256 key. One key for the whole fleet, fetched once at server
// startup and cached in memory for the process lifetime — never re-fetched
// per request or per encrypt/decrypt call (plan §3.3).
func MasterKeyFromHex(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("asaas: master key is not valid hex: %w", err)
	}
	if len(key) != MasterKeySize {
		return nil, fmt.Errorf("asaas: master key must be %d bytes, got %d", MasterKeySize, len(key))
	}
	return key, nil
}

// EncryptAPIKey encrypts a subaccount's Asaas API key under masterKey with
// AES-256-GCM, a random nonce per encryption. Returns ciphertext and nonce
// separately (matching BaasAccount.APIKeyCiphertext/APIKeyNonce) rather than
// prepending the nonce, so the ciphertext is a clean fixed-format blob.
func EncryptAPIKey(masterKey []byte, plaintext string) (ciphertext, nonce []byte, err error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("asaas: nonce generation failed: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// DecryptAPIKey reverses EncryptAPIKey.
func DecryptAPIKey(masterKey, ciphertext, nonce []byte) (string, error) {
	gcm, err := newGCM(masterKey)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", errors.New("asaas: invalid nonce size")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("asaas: decrypt failed: %w", err)
	}
	return string(plaintext), nil
}

func newGCM(masterKey []byte) (cipher.AEAD, error) {
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("asaas: master key must be %d bytes, got %d", MasterKeySize, len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("asaas: aes cipher init failed: %w", err)
	}
	return cipher.NewGCM(block)
}
