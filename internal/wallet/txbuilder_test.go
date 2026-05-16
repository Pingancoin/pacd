package wallet_test

import (
	"testing"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/txscript"
	"github.com/Pingancoin/pacd/internal/wallet"
)

func TestParsePACAmount(t *testing.T) {
	tests := map[string]int64{
		"0.00000001": 1,
		"1":          100_000_000,
		"1.23":       123_000_000,
	}
	for input, want := range tests {
		got, err := wallet.ParsePACAmount(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("ParsePACAmount(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestBuildDraftTx(t *testing.T) {
	params := chaincfg.SimNetParams()
	w, err := wallet.Create(wallet.Path(t.TempDir(), params.Name), params)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddKey(params, "dest"); err != nil {
		t.Fatal(err)
	}

	balance := wallet.Balance{
		UTXOs: []wallet.UTXO{{
			Address: w.Keys[0].Address,
			TxHash:  "0000000000000000000000000000000000000000000000000000000000000001",
			Vout:    0,
			Value:   100_000_000,
			Height:  1,
		}},
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, 40_000_000, 1_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Tx.TxIn) != 1 {
		t.Fatalf("inputs = %d, want 1", len(draft.Tx.TxIn))
	}
	if len(draft.Tx.TxOut) != 2 {
		t.Fatalf("outputs = %d, want 2", len(draft.Tx.TxOut))
	}
	if draft.Change != 59_999_000 {
		t.Fatalf("change = %d", draft.Change)
	}
}

func TestSignDraftTx(t *testing.T) {
	params := chaincfg.SimNetParams()
	w, err := wallet.Create(wallet.Path(t.TempDir(), params.Name), params)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddKey(params, "dest"); err != nil {
		t.Fatal(err)
	}

	balance := wallet.Balance{
		UTXOs: []wallet.UTXO{{
			Address: w.Keys[0].Address,
			TxHash:  "0000000000000000000000000000000000000000000000000000000000000001",
			Vout:    0,
			Value:   100_000_000,
			Height:  1,
		}},
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, 40_000_000, 1_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}
	if len(draft.Tx.TxIn[0].SignatureScript) == 0 {
		t.Fatal("signature script was not populated")
	}
	prevScript := mustDecodeAddressScript(t, params, w.Keys[0].Address)
	if err := txscript.VerifyP2PKHInput(draft.Tx, 0, prevScript); err != nil {
		t.Fatal(err)
	}
}

func mustDecodeAddressScript(t *testing.T, params *chaincfg.Params, addr string) []byte {
	t.Helper()
	script, err := address.DecodeAddressScript(params, addr)
	if err != nil {
		t.Fatal(err)
	}
	return script
}
