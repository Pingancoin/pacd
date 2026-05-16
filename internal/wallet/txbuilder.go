package wallet

import (
	"encoding/hex"
	"fmt"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/txscript"
	"github.com/Pingancoin/pacd/internal/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

type DraftTx struct {
	Tx          *wire.MsgTx
	Selected    []UTXO
	InputTotal  int64
	OutputTotal int64
	Fee         int64
	Change      int64
	ChangeAddr  string
	Destination string
}

func BuildDraftTx(params *chaincfg.Params, w *Wallet, balance Balance, destination string, amount int64, fee int64, changeAddr string) (DraftTx, error) {
	if amount <= 0 {
		return DraftTx{}, fmt.Errorf("amount must be positive")
	}
	if fee < 0 {
		return DraftTx{}, fmt.Errorf("fee must not be negative")
	}
	if changeAddr == "" {
		if len(w.Keys) == 0 {
			return DraftTx{}, fmt.Errorf("wallet has no change address")
		}
		changeAddr = w.Keys[0].Address
	}

	destScript, err := address.DecodeAddressScript(params, destination)
	if err != nil {
		return DraftTx{}, fmt.Errorf("destination address: %w", err)
	}
	changeScript, err := address.DecodeAddressScript(params, changeAddr)
	if err != nil {
		return DraftTx{}, fmt.Errorf("change address: %w", err)
	}

	need := amount + fee
	if need < amount {
		return DraftTx{}, fmt.Errorf("amount overflow")
	}
	selected, inputTotal, err := selectUTXOs(balance.UTXOs, need)
	if err != nil {
		return DraftTx{}, err
	}
	change := inputTotal - need

	tx := &wire.MsgTx{Version: 1}
	for _, utxo := range selected {
		hashBytes, err := hex.DecodeString(utxo.TxHash)
		if err != nil {
			return DraftTx{}, err
		}
		hash, err := wire.NewHashFromBytes(hashBytes)
		if err != nil {
			return DraftTx{}, err
		}
		tx.TxIn = append(tx.TxIn, &wire.TxIn{
			PreviousOutPoint: wire.OutPoint{
				Hash:  hash,
				Index: utxo.Vout,
			},
			Sequence: wire.MaxUint32,
		})
	}
	tx.TxOut = append(tx.TxOut, &wire.TxOut{
		Value:    amount,
		PkScript: destScript,
	})
	if change > 0 {
		tx.TxOut = append(tx.TxOut, &wire.TxOut{
			Value:    change,
			PkScript: changeScript,
		})
	}

	return DraftTx{
		Tx:          tx,
		Selected:    selected,
		InputTotal:  inputTotal,
		OutputTotal: amount + change,
		Fee:         fee,
		Change:      change,
		ChangeAddr:  changeAddr,
		Destination: destination,
	}, nil
}

func SignDraftTx(params *chaincfg.Params, w *Wallet, draft DraftTx) error {
	keysByAddress := make(map[string]Key, len(w.Keys))
	for _, key := range w.Keys {
		keysByAddress[key.Address] = key
	}
	if len(draft.Selected) != len(draft.Tx.TxIn) {
		return fmt.Errorf("selected utxo count does not match input count")
	}
	for inputIndex, utxo := range draft.Selected {
		key, ok := keysByAddress[utxo.Address]
		if !ok {
			return fmt.Errorf("no wallet key for %s", utxo.Address)
		}
		privBytes, err := hex.DecodeString(key.PrivKeyHex)
		if err != nil {
			return err
		}
		priv := secp256k1.PrivKeyFromBytes(privBytes)
		prevPkScript, err := address.DecodeAddressScript(params, utxo.Address)
		if err != nil {
			return err
		}
		if err := txscript.SignP2PKHInput(draft.Tx, inputIndex, prevPkScript, priv); err != nil {
			return err
		}
	}
	return nil
}

func selectUTXOs(utxos []UTXO, need int64) ([]UTXO, int64, error) {
	var selected []UTXO
	var total int64
	for _, utxo := range utxos {
		selected = append(selected, utxo)
		total += utxo.Value
		if total >= need {
			return selected, total, nil
		}
	}
	return nil, 0, fmt.Errorf("insufficient funds: need %d atoms, have %d atoms", need, total)
}
