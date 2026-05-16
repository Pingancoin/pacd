package wallet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const FileName = "wallet.json"

type Wallet struct {
	Version    int         `json:"version"`
	Network    string      `json:"network"`
	CreatedAt  time.Time   `json:"created_at"`
	Encryption *Encryption `json:"encryption,omitempty"`
	Keys       []Key       `json:"keys"`
}

type Key struct {
	Label               string    `json:"label"`
	Address             string    `json:"address"`
	PubKeyHex           string    `json:"pubkey_hex"`
	PrivKeyHex          string    `json:"privkey_hex,omitempty"`
	EncryptedPrivKeyHex string    `json:"encrypted_privkey_hex,omitempty"`
	NonceHex            string    `json:"nonce_hex,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pacwallet"
	}
	return filepath.Join(home, ".pacwallet")
}

func Path(dir, network string) string {
	return filepath.Join(dir, network, FileName)
}

func Create(path string, params *chaincfg.Params) (*Wallet, error) {
	return create(path, params, "")
}

func CreateEncrypted(path string, params *chaincfg.Params, passphrase string) (*Wallet, error) {
	return create(path, params, passphrase)
}

func create(path string, params *chaincfg.Params, passphrase string) (*Wallet, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("wallet already exists at %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	w := &Wallet{
		Version:   1,
		Network:   params.Name,
		CreatedAt: time.Now().UTC(),
	}
	if passphrase != "" {
		encryption, err := newEncryption(passphrase)
		if err != nil {
			return nil, err
		}
		w.Encryption = encryption
	}
	if err := w.AddKeyWithPassphrase(params, "default", passphrase); err != nil {
		return nil, err
	}
	return w, Save(path, w)
}

func Load(path string) (*Wallet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w Wallet
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func Save(path string, w *Wallet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func (w *Wallet) AddKey(params *chaincfg.Params, label string) error {
	return w.AddKeyWithPassphrase(params, label, "")
}

func (w *Wallet) AddKeyWithPassphrase(params *chaincfg.Params, label string, passphrase string) error {
	if w.Network != "" && w.Network != params.Name {
		return fmt.Errorf("wallet network %q does not match params %q", w.Network, params.Name)
	}
	if label == "" {
		label = fmt.Sprintf("key-%d", len(w.Keys)+1)
	}
	priv, err := newPrivateKey()
	if err != nil {
		return err
	}
	return w.addPrivateKey(params, label, priv.Serialize(), passphrase)
}

func (w *Wallet) ImportPrivateKey(params *chaincfg.Params, label string, privKeyHex string, passphrase string) (Key, error) {
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return Key{}, fmt.Errorf("private key hex: %w", err)
	}
	if len(privBytes) != 32 {
		return Key{}, fmt.Errorf("private key length is %d, want 32", len(privBytes))
	}
	if w.Network != "" && w.Network != params.Name {
		return Key{}, fmt.Errorf("wallet network %q does not match params %q", w.Network, params.Name)
	}
	for _, existing := range w.Keys {
		existingBytes, err := w.PrivateKeyBytes(existing, passphrase)
		if err == nil && hex.EncodeToString(existingBytes) == hex.EncodeToString(privBytes) {
			return Key{}, fmt.Errorf("private key already exists for %s", existing.Address)
		}
	}
	if err := w.addPrivateKey(params, label, privBytes, passphrase); err != nil {
		return Key{}, err
	}
	return w.Keys[len(w.Keys)-1], nil
}

func (w *Wallet) Encrypt(passphrase string) error {
	if w.IsEncrypted() {
		return fmt.Errorf("wallet is already encrypted")
	}
	encryption, err := newEncryption(passphrase)
	if err != nil {
		return err
	}
	w.Encryption = encryption
	for i := range w.Keys {
		privBytes, err := hex.DecodeString(w.Keys[i].PrivKeyHex)
		if err != nil {
			return fmt.Errorf("wallet key %q: %w", w.Keys[i].Label, err)
		}
		if err := w.encryptKey(&w.Keys[i], privBytes, passphrase); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wallet) ChangePassphrase(oldPassphrase string, newPassphrase string) error {
	if !w.IsEncrypted() {
		return fmt.Errorf("wallet is not encrypted")
	}
	if err := validatePassphrase(newPassphrase); err != nil {
		return err
	}
	privKeys := make([][]byte, len(w.Keys))
	for i, key := range w.Keys {
		privBytes, err := w.PrivateKeyBytes(key, oldPassphrase)
		if err != nil {
			return err
		}
		privKeys[i] = privBytes
	}
	encryption, err := newEncryption(newPassphrase)
	if err != nil {
		return err
	}
	w.Encryption = encryption
	for i := range w.Keys {
		if err := w.encryptKey(&w.Keys[i], privKeys[i], newPassphrase); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wallet) addPrivateKey(params *chaincfg.Params, label string, privBytes []byte, passphrase string) error {
	if w.Network != "" && w.Network != params.Name {
		return fmt.Errorf("wallet network %q does not match params %q", w.Network, params.Name)
	}
	if label == "" {
		label = fmt.Sprintf("key-%d", len(w.Keys)+1)
	}
	priv := secp256k1.PrivKeyFromBytes(privBytes)
	if priv.Key.IsZero() {
		return fmt.Errorf("private key is zero")
	}
	pubKey := priv.PubKey().SerializeCompressed()
	addr, _, _, err := address.AddressFromPubKey(params, pubKey)
	if err != nil {
		return err
	}
	for _, existing := range w.Keys {
		if existing.Address == addr {
			return fmt.Errorf("address %s already exists in wallet", addr)
		}
	}
	key := Key{
		Label:     label,
		Address:   addr,
		PubKeyHex: hex.EncodeToString(pubKey),
		CreatedAt: time.Now().UTC(),
	}
	if w.IsEncrypted() {
		if err := w.encryptKey(&key, priv.Serialize(), passphrase); err != nil {
			return err
		}
	} else {
		key.PrivKeyHex = hex.EncodeToString(priv.Serialize())
	}
	w.Keys = append(w.Keys, key)
	return nil
}

func (w *Wallet) encryptKey(key *Key, privBytes []byte, passphrase string) error {
	derived, err := w.Encryption.deriveKey(passphrase)
	if err != nil {
		return err
	}
	encryptedPrivKeyHex, nonceHex, err := encryptSecret(derived, privBytes)
	if err != nil {
		return err
	}
	key.PrivKeyHex = ""
	key.EncryptedPrivKeyHex = encryptedPrivKeyHex
	key.NonceHex = nonceHex
	return nil
}

func (w *Wallet) IsEncrypted() bool {
	return w.Encryption != nil
}

func (w *Wallet) PrivateKeyBytes(key Key, passphrase string) ([]byte, error) {
	if w.IsEncrypted() {
		derived, err := w.Encryption.deriveKey(passphrase)
		if err != nil {
			return nil, err
		}
		if key.EncryptedPrivKeyHex == "" || key.NonceHex == "" {
			return nil, fmt.Errorf("wallet key %q is missing encrypted private key data", key.Label)
		}
		return decryptSecret(derived, key.EncryptedPrivKeyHex, key.NonceHex)
	}
	if key.PrivKeyHex == "" {
		return nil, fmt.Errorf("wallet key %q has no private key", key.Label)
	}
	return hex.DecodeString(key.PrivKeyHex)
}

func (w *Wallet) PrivateKeyHex(key Key, passphrase string) (string, error) {
	privBytes, err := w.PrivateKeyBytes(key, passphrase)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(privBytes), nil
}

func newPrivateKey() (*secp256k1.PrivateKey, error) {
	for {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		priv := secp256k1.PrivKeyFromBytes(b[:])
		if !priv.Key.IsZero() {
			return priv, nil
		}
	}
}
