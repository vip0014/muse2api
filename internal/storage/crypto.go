package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

type sealBox struct{ key []byte }

func newSealBox(key []byte) (*sealBox, error) {
	if len(key) != 32 {
		return nil, errors.New("credential encryption requires a 32-byte key")
	}
	return &sealBox{key: append([]byte(nil), key...)}, nil
}
func (b *sealBox) Seal(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plaintext, []byte("muse2api.accounts.v1"))
	return base64.StdEncoding.EncodeToString(out), nil
}
func (b *sealBox) Open(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("encrypted credential payload is truncated")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("muse2api.accounts.v1"))
	if err != nil {
		return nil, fmt.Errorf("无法解密账号凭据，请检查 MUSE2API_CREDENTIAL_KEY: %w", err)
	}
	return plaintext, nil
}
