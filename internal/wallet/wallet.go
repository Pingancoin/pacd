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
	Version   int       `json:"version"`
	Network   string    `json:"network"`
	CreatedAt time.Time `json:"created_at"`
	Keys      []Key     `json:"keys"`
}

type Key struct {
	Label      string    `json:"label"`
	Address    string    `json:"address"`
	PubKeyHex  string    `json:"pubkey_hex"`
	PrivKeyHex string    `json:"privkey_hex"`
	CreatedAt  time.Time `json:"created_at"`
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
	if err := w.AddKey(params, "default"); err != nil {
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
	pubKey := priv.PubKey().SerializeCompressed()
	addr, _, _, err := address.AddressFromPubKey(params, pubKey)
	if err != nil {
		return err
	}
	w.Keys = append(w.Keys, Key{
		Label:      label,
		Address:    addr,
		PubKeyHex:  hex.EncodeToString(pubKey),
		PrivKeyHex: hex.EncodeToString(priv.Serialize()),
		CreatedAt:  time.Now().UTC(),
	})
	return nil
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
