package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	encryptionKDF       = "argon2id"
	encryptionCipher    = "aes-256-gcm"
	argonTime           = uint32(3)
	argonMemoryKiB      = uint32(64 * 1024)
	argonThreads        = uint8(4)
	argonKeyLen         = uint32(32)
	encryptionSaltSize  = 16
	encryptionNonceSize = 12
	minPassphraseRunes  = 8
)

type Encryption struct {
	KDF       string `json:"kdf"`
	Cipher    string `json:"cipher"`
	SaltHex   string `json:"salt_hex"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	KeyLen    uint32 `json:"key_len"`
}

func newEncryption(passphrase string) (*Encryption, error) {
	if err := validatePassphrase(passphrase); err != nil {
		return nil, err
	}
	salt := make([]byte, encryptionSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	return &Encryption{
		KDF:       encryptionKDF,
		Cipher:    encryptionCipher,
		SaltHex:   hex.EncodeToString(salt),
		Time:      argonTime,
		MemoryKiB: argonMemoryKiB,
		Threads:   argonThreads,
		KeyLen:    argonKeyLen,
	}, nil
}

func (e *Encryption) deriveKey(passphrase string) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("wallet is not encrypted")
	}
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase is required")
	}
	if e.KDF != encryptionKDF {
		return nil, fmt.Errorf("unsupported wallet KDF %q", e.KDF)
	}
	if e.Cipher != encryptionCipher {
		return nil, fmt.Errorf("unsupported wallet cipher %q", e.Cipher)
	}
	salt, err := hex.DecodeString(e.SaltHex)
	if err != nil {
		return nil, fmt.Errorf("wallet salt: %w", err)
	}
	if e.KeyLen != argonKeyLen {
		return nil, fmt.Errorf("unsupported wallet key length %d", e.KeyLen)
	}
	return argon2.IDKey([]byte(passphrase), salt, e.Time, e.MemoryKiB, e.Threads, e.KeyLen), nil
}

func encryptSecret(key []byte, plain []byte) (cipherHex string, nonceHex string, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, encryptionNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	sealed := aead.Seal(nil, nonce, plain, nil)
	return hex.EncodeToString(sealed), hex.EncodeToString(nonce), nil
}

func decryptSecret(key []byte, cipherHex string, nonceHex string) ([]byte, error) {
	sealed, err := hex.DecodeString(cipherHex)
	if err != nil {
		return nil, fmt.Errorf("encrypted secret: %w", err)
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return nil, fmt.Errorf("encrypted nonce: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("encrypted nonce length is %d, want %d", len(nonce), aead.NonceSize())
	}
	plain, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase or corrupted wallet secret")
	}
	return plain, nil
}

func validatePassphrase(passphrase string) error {
	if len([]rune(passphrase)) < minPassphraseRunes {
		return fmt.Errorf("passphrase must be at least %d characters", minPassphraseRunes)
	}
	return nil
}
