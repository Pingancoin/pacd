package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Pingancoin/pacd/internal/wire"
)

type SubmitResult struct {
	Accepted    bool   `json:"accepted"`
	TxID        string `json:"txid"`
	MempoolSize int    `json:"mempoolsize"`
}

func SubmitRawTransaction(rpcURL string, tx *wire.MsgTx) (SubmitResult, error) {
	serialized, err := tx.Serialize()
	if err != nil {
		return SubmitResult{}, err
	}
	reqBody, err := json.Marshal(map[string]string{
		"txhex": hex.EncodeToString(serialized),
	})
	if err != nil {
		return SubmitResult{}, err
	}

	url := strings.TrimRight(rpcURL, "/") + "/submitrawtransaction"
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return SubmitResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var rpcErr struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rpcErr); err == nil && rpcErr.Error != "" {
			return SubmitResult{}, fmt.Errorf("%s returned %s: %s", url, resp.Status, rpcErr.Error)
		}
		return SubmitResult{}, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	var result SubmitResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}
