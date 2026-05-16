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
