// Package crypto implements AES-256-CTR envelope encryption for Kerebrom databases.
// Compatible with the Python format: MAGIC + ITERATIONS + SALT + IV + MAC + CIPHERTEXT.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/pbkdf2"
)

const (
	Magic      = "KEREBROM-ENC-1"
	MagicLen   = 14
	SaltSize   = 16
	IVSize     = 16
	MACSize    = 32
	Iterations = 200_000
	KeyLen     = 64 // 32 enc + 32 mac
)

// IsEncryptedContainer checks if a file starts with the Kerebrom magic bytes.
func IsEncryptedContainer(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, MagicLen)
	n, _ := f.Read(buf)
	return n == MagicLen && string(buf) == Magic
}

// EncryptFile encrypts plainPath into encPath using the given passphrase.
func EncryptFile(plainPath, encPath, passphrase string) error {
	plaintext, err := os.ReadFile(plainPath)
	if err != nil {
		return fmt.Errorf("read plain: %w", err)
	}

	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	iv := make([]byte, IVSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return err
	}

	encKey, macKey := deriveKeys(passphrase, salt, Iterations)

	// AES-256-CTR encryption.
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return err
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext, plaintext)

	// Build header.
	iterBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(iterBytes, uint32(Iterations))

	// HMAC-SHA256 over header + ciphertext.
	mac := computeMAC(macKey, []byte(Magic), iterBytes, salt, iv, ciphertext)

	// Write container: MAGIC | ITERATIONS | SALT | IV | MAC | CIPHERTEXT
	f, err := os.Create(encPath)
	if err != nil {
		return err
	}
	defer f.Close()
	f.Write([]byte(Magic))
	f.Write(iterBytes)
	f.Write(salt)
	f.Write(iv)
	f.Write(mac)
	f.Write(ciphertext)
	return nil
}

// DecryptFile decrypts encPath into plainPath using the given passphrase.
func DecryptFile(encPath, plainPath, passphrase string) error {
	data, err := os.ReadFile(encPath)
	if err != nil {
		return fmt.Errorf("read enc: %w", err)
	}
	minLen := MagicLen + 4 + SaltSize + IVSize + MACSize
	if len(data) < minLen {
		return errors.New("file too short to be encrypted container")
	}
	if string(data[:MagicLen]) != Magic {
		return errors.New("not a Kerebrom encrypted container")
	}

	offset := MagicLen
	iterations := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	salt := data[offset : offset+SaltSize]
	offset += SaltSize
	iv := data[offset : offset+IVSize]
	offset += IVSize
	storedMAC := data[offset : offset+MACSize]
	offset += MACSize
	ciphertext := data[offset:]

	encKey, macKey := deriveKeys(passphrase, salt, iterations)

	// Verify MAC.
	iterBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(iterBytes, uint32(iterations))
	expectedMAC := computeMAC(macKey, []byte(Magic), iterBytes, salt, iv, ciphertext)
	if !hmac.Equal(storedMAC, expectedMAC) {
		return errors.New("invalid passphrase or corrupted container")
	}

	// Decrypt.
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCTR(block, iv).XORKeyStream(plaintext, ciphertext)

	return os.WriteFile(plainPath, plaintext, 0600)
}

func deriveKeys(passphrase string, salt []byte, iterations int) (encKey, macKey []byte) {
	material := pbkdf2.Key([]byte(passphrase), salt, iterations, KeyLen, sha256.New)
	return material[:32], material[32:]
}

func computeMAC(macKey []byte, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, macKey)
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}
