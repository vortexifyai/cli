package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func GenerateVerifier() (string, error) { return randomString() }
func GenerateState() (string, error)    { return randomString() }
func Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
