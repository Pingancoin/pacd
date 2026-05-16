package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
)

type Balance struct {
	Total      int64  `json:"total"`
	Spendable  int64  `json:"spendable"`
	UTXOCount  int    `json:"utxo_count"`
	BestHeight uint32 `json:"best_height"`
	BestHash   string `json:"best_hash"`
	UTXOs      []UTXO `json:"utxos"`
}

type UTXO struct {
	Address string `json:"address"`
	TxHash  string `json:"tx_hash"`
	Vout    uint32 `json:"vout"`
	Value   int64  `json:"value"`
	Height  uint32 `json:"height"`
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
	Hash string   `json:"hash"`
	Vin  []rpcIn  `json:"vin"`
	Vout []rpcOut `json:"vout"`
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

func ScanBalance(params *chaincfg.Params, w *Wallet, rpcURL string) (Balance, error) {
	rpcURL = strings.TrimRight(rpcURL, "/")
	var tip chainTip
	if err := getJSON(rpcURL+"/getbestblock", &tip); err != nil {
		return Balance{}, err
	}

	watched, err := watchedScripts(params, w)
	if err != nil {
		return Balance{}, err
	}

	utxos := make(map[string]UTXO)
	for height := uint32(0); height <= tip.Height; height++ {
		var block rpcBlock
		if err := getJSON(fmt.Sprintf("%s/getblock/%d", rpcURL, height), &block); err != nil {
			return Balance{}, err
		}
		for _, tx := range block.Tx {
			for _, vin := range tx.Vin {
				delete(utxos, outpointKey(vin.Hash, vin.Index))
			}
			for _, vout := range tx.Vout {
				addressLabel, ok := watched[strings.ToLower(vout.PkScript)]
				if !ok {
					continue
				}
				utxos[outpointKey(tx.Hash, vout.N)] = UTXO{
					Address: addressLabel,
					TxHash:  tx.Hash,
					Vout:    vout.N,
					Value:   vout.Value,
					Height:  block.Height,
				}
			}
		}
	}

	result := Balance{
		BestHeight: tip.Height,
		BestHash:   tip.Hash,
		UTXOs:      make([]UTXO, 0, len(utxos)),
	}
	for _, utxo := range utxos {
		result.Total += utxo.Value
		result.Spendable += utxo.Value
		result.UTXOs = append(result.UTXOs, utxo)
	}
	result.UTXOCount = len(result.UTXOs)
	return result, nil
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
