package service

import (
	"errors"
	"strings"
)

// reauthTestEncryptor is a deterministic, reversible stand-in for the credential
// encryptor, used by the cyber-evidence tests (ranxi keeps it in its OAuth
// re-authentication tests, which Kitty does not carry).
type reauthTestEncryptor struct{}

func (reauthTestEncryptor) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (reauthTestEncryptor) Decrypt(value string) (string, error) {
	plain, ok := strings.CutPrefix(value, "encrypted:")
	if !ok {
		return "", errors.New("invalid ciphertext")
	}
	return plain, nil
}
