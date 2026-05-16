package wallet_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wallet"
)

func TestCreateLoadAndAddKey(t *testing.T) {
	params := chaincfg.SimNetParams()
	path := wallet.Path(t.TempDir(), params.Name)

	w, err := wallet.Create(path, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(w.Keys))
	}
	if !strings.HasPrefix(w.Keys[0].Address, "S") {
		t.Fatalf("address = %s, want S prefix", w.Keys[0].Address)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("wallet perms = %o, want 600", got)
	}

	loaded, err := wallet.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.AddKey(params, "second"); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(loaded.Keys))
	}
	if err := wallet.Save(path, loaded); err != nil {
		t.Fatal(err)
	}
}

func TestCreateEncryptedWallet(t *testing.T) {
	params := chaincfg.SimNetParams()
	path := wallet.Path(t.TempDir(), params.Name)
	const passphrase = "correct horse battery staple"

	w, err := wallet.CreateEncrypted(path, params, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !w.IsEncrypted() {
		t.Fatal("wallet is not encrypted")
	}
	if w.Keys[0].PrivKeyHex != "" {
		t.Fatal("encrypted wallet stored plaintext private key")
	}
	if w.Keys[0].EncryptedPrivKeyHex == "" || w.Keys[0].NonceHex == "" {
		t.Fatal("encrypted wallet key is missing ciphertext or nonce")
	}

	loaded, err := wallet.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.PrivateKeyBytes(loaded.Keys[0], "wrong passphrase"); err == nil {
		t.Fatal("expected wrong passphrase to fail")
	}
	priv, err := loaded.PrivateKeyBytes(loaded.Keys[0], passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if len(priv) != 32 {
		t.Fatalf("private key length = %d, want 32", len(priv))
	}
	if err := loaded.AddKeyWithPassphrase(params, "second", passphrase); err != nil {
		t.Fatal(err)
	}
	if loaded.Keys[1].PrivKeyHex != "" {
		t.Fatal("new encrypted key stored plaintext private key")
	}
}

func TestEncryptedWalletRequiresPassphraseForNewKey(t *testing.T) {
	params := chaincfg.SimNetParams()
	w, err := wallet.CreateEncrypted(wallet.Path(t.TempDir(), params.Name), params, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddKey(params, "no-passphrase"); err == nil {
		t.Fatal("expected AddKey without passphrase to fail on encrypted wallet")
	}
}

func TestEncryptPlaintextWalletAndChangePassphrase(t *testing.T) {
	params := chaincfg.SimNetParams()
	path := wallet.Path(t.TempDir(), params.Name)

	w, err := wallet.Create(path, params)
	if err != nil {
		t.Fatal(err)
	}
	plainKey := w.Keys[0].PrivKeyHex
	if plainKey == "" {
		t.Fatal("expected plaintext test wallet")
	}
	if err := w.Encrypt("correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if w.Keys[0].PrivKeyHex != "" {
		t.Fatal("plaintext private key survived encryption")
	}
	if got, err := w.PrivateKeyHex(w.Keys[0], "correct horse battery staple"); err != nil || got != plainKey {
		t.Fatalf("decrypted key = %s, err = %v", got, err)
	}
	if err := w.ChangePassphrase("correct horse battery staple", "new correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PrivateKeyHex(w.Keys[0], "correct horse battery staple"); err == nil {
		t.Fatal("old passphrase still decrypted wallet")
	}
	if got, err := w.PrivateKeyHex(w.Keys[0], "new correct horse battery staple"); err != nil || got != plainKey {
		t.Fatalf("decrypted key after passphrase change = %s, err = %v", got, err)
	}
}

func TestImportPrivateKey(t *testing.T) {
	params := chaincfg.SimNetParams()
	source, err := wallet.Create(wallet.Path(t.TempDir(), params.Name), params)
	if err != nil {
		t.Fatal(err)
	}
	privKeyHex := source.Keys[0].PrivKeyHex

	dest, err := wallet.CreateEncrypted(wallet.Path(t.TempDir(), params.Name), params, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := dest.ImportPrivateKey(params, "imported", privKeyHex, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if imported.Address != source.Keys[0].Address {
		t.Fatalf("imported address = %s, want %s", imported.Address, source.Keys[0].Address)
	}
	if imported.PrivKeyHex != "" {
		t.Fatal("imported encrypted key stored plaintext")
	}
	if got, err := dest.PrivateKeyHex(imported, "correct horse battery staple"); err != nil || got != privKeyHex {
		t.Fatalf("imported private key = %s, err = %v", got, err)
	}
}
