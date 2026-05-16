package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
)

type Balance struct {
	Total      int64  `json:"total"`
	Spendable  int64  `json:"spendable"`
	Immature   int64  `json:"immature"`
	Pending    int64  `json:"pending"`
	UTXOCount  int    `json:"utxo_count"`
	BestHeight uint32 `json:"best_height"`
	BestHash   string `json:"best_hash"`
	UTXOs      []UTXO `json:"utxos"`
}

type HistoryEntry struct {
	TxHash    string   `json:"tx_hash"`
	Height    uint32   `json:"height"`
	Pending   bool     `json:"pending"`
	Coinbase  bool     `json:"coinbase"`
	Received  int64    `json:"received"`
	Sent      int64    `json:"sent"`
	Net       int64    `json:"net"`
	Addresses []string `json:"addresses"`
}

type WalletView struct {
	Balance Balance        `json:"balance"`
	History []HistoryEntry `json:"history"`
}

type UTXO struct {
	Address  string `json:"address"`
	TxHash   string `json:"tx_hash"`
	Vout     uint32 `json:"vout"`
	Value    int64  `json:"value"`
	Height   uint32 `json:"height"`
	Coinbase bool   `json:"coinbase"`
	Mature   bool   `json:"mature"`
	Pending  bool   `json:"pending"`
}

type chainTip struct {
	Height uint32 `json:"height"`
	Hash   string `json:"hash"`
}

type rpcBlock struct {
	Height uint32  `json:"height"`
	Hash   string  `json:"hash"`
	Tx     []rpcTx `json:"tx"`
}

type rpcTx struct {
	Hash     string   `json:"hash"`
	Coinbase bool     `json:"coinbase"`
	Vin      []rpcIn  `json:"vin"`
	Vout     []rpcOut `json:"vout"`
}

type rpcIn struct {
	Hash  string `json:"hash"`
	Index uint32 `json:"index"`
}

type rpcOut struct {
	N        uint32 `json:"n"`
	Value    int64  `json:"value"`
	PkScript string `json:"pkscript"`
}

type rpcMempool struct {
	Size int     `json:"size"`
	Tx   []rpcTx `json:"tx"`
}

func ScanBalance(params *chaincfg.Params, w *Wallet, rpcURL string) (Balance, error) {
	view, err := ScanWallet(params, w, rpcURL)
	if err != nil {
		return Balance{}, err
	}
	return view.Balance, nil
}

func ScanHistory(params *chaincfg.Params, w *Wallet, rpcURL string) ([]HistoryEntry, error) {
	view, err := ScanWallet(params, w, rpcURL)
	if err != nil {
		return nil, err
	}
	return view.History, nil
}

func ScanWallet(params *chaincfg.Params, w *Wallet, rpcURL string) (WalletView, error) {
	rpcURL = strings.TrimRight(rpcURL, "/")
	var tip chainTip
	if err := getJSON(rpcURL+"/getbestblock", &tip); err != nil {
		return WalletView{}, err
	}

	watched, err := watchedScripts(params, w)
	if err != nil {
		return WalletView{}, err
	}

	utxos := make(map[string]UTXO)
	var history []HistoryEntry
	for height := uint32(0); height <= tip.Height; height++ {
		var block rpcBlock
		if err := getJSON(fmt.Sprintf("%s/getblock/%d", rpcURL, height), &block); err != nil {
			return WalletView{}, err
		}
		for _, tx := range block.Tx {
			applyTx(params, watched, utxos, tx, block.Height, tip.Height, false, &history)
		}
	}
	if err := applyMempool(params, rpcURL, watched, utxos, tip.Height, &history); err != nil {
		return WalletView{}, err
	}

	result := Balance{
		BestHeight: tip.Height,
		BestHash:   tip.Hash,
		UTXOs:      make([]UTXO, 0, len(utxos)),
	}
	for _, utxo := range utxos {
		result.Total += utxo.Value
		switch {
		case utxo.Pending:
			result.Pending += utxo.Value
		case utxo.Mature:
			result.Spendable += utxo.Value
		default:
			result.Immature += utxo.Value
		}
		result.UTXOs = append(result.UTXOs, utxo)
	}
	sort.Slice(result.UTXOs, func(i, j int) bool {
		if result.UTXOs[i].Height != result.UTXOs[j].Height {
			return result.UTXOs[i].Height < result.UTXOs[j].Height
		}
		if result.UTXOs[i].TxHash != result.UTXOs[j].TxHash {
			return result.UTXOs[i].TxHash < result.UTXOs[j].TxHash
		}
		return result.UTXOs[i].Vout < result.UTXOs[j].Vout
	})
	result.UTXOCount = len(result.UTXOs)
	return WalletView{Balance: result, History: history}, nil
}

func applyMempool(params *chaincfg.Params, rpcURL string, watched map[string]string, utxos map[string]UTXO, tipHeight uint32, history *[]HistoryEntry) error {
	var mempool rpcMempool
	if err := getJSON(rpcURL+"/getrawmempool", &mempool); err != nil {
		return err
	}
	for _, tx := range mempool.Tx {
		applyTx(params, watched, utxos, tx, 0, tipHeight, true, history)
	}
	return nil
}

func applyTx(params *chaincfg.Params, watched map[string]string, utxos map[string]UTXO, tx rpcTx, height uint32, tipHeight uint32, pending bool, history *[]HistoryEntry) {
	var sent int64
	var received int64
	addressSet := make(map[string]struct{})

	for _, vin := range tx.Vin {
		key := outpointKey(vin.Hash, vin.Index)
		utxo, ok := utxos[key]
		if !ok {
			continue
		}
		sent += utxo.Value
		addressSet[utxo.Address] = struct{}{}
		delete(utxos, key)
	}
	for _, vout := range tx.Vout {
		addressLabel, ok := watched[strings.ToLower(vout.PkScript)]
		if !ok {
			continue
		}
		received += vout.Value
		addressSet[addressLabel] = struct{}{}
		utxos[outpointKey(tx.Hash, vout.N)] = UTXO{
			Address:  addressLabel,
			TxHash:   tx.Hash,
			Vout:     vout.N,
			Value:    vout.Value,
			Height:   height,
			Coinbase: tx.Coinbase,
			Mature:   !pending && isMature(params, tx.Coinbase, height, tipHeight),
			Pending:  pending,
		}
	}
	if received == 0 && sent == 0 {
		return
	}
	addresses := make([]string, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)
	*history = append(*history, HistoryEntry{
		TxHash:    tx.Hash,
		Height:    height,
		Pending:   pending,
		Coinbase:  tx.Coinbase,
		Received:  received,
		Sent:      sent,
		Net:       received - sent,
		Addresses: addresses,
	})
}

func isMature(params *chaincfg.Params, coinbase bool, height uint32, tipHeight uint32) bool {
	if !coinbase {
		return true
	}
	return tipHeight+1 >= height+params.CoinbaseMaturity
}

func watchedScripts(params *chaincfg.Params, w *Wallet) (map[string]string, error) {
	watched := make(map[string]string, len(w.Keys))
	for _, key := range w.Keys {
		pubKey, err := hex.DecodeString(key.PubKeyHex)
		if err != nil {
			return nil, fmt.Errorf("wallet pubkey %q: %w", key.Label, err)
		}
		_, _, pkScript, err := address.AddressFromPubKey(params, pubKey)
		if err != nil {
			return nil, err
		}
		watched[hex.EncodeToString(pkScript)] = key.Address
	}
	return watched, nil
}

func outpointKey(hash string, index uint32) string {
	return fmt.Sprintf("%s:%d", hash, index)
}

func getJSON(url string, dest any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}
