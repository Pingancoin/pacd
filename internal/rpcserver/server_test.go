package rpcserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/rpcserver"
	"github.com/Pingancoin/pacd/internal/wallet"
)

func TestHandlers(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 2; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var count struct {
		Height uint32 `json:"height"`
	}
	getJSON(t, httpServer.URL+"/getblockcount", &count)
	if count.Height != 2 {
		t.Fatalf("height = %d, want 2", count.Height)
	}

	var best struct {
		Height uint32 `json:"height"`
		Hash   string `json:"hash"`
	}
	getJSON(t, httpServer.URL+"/getbestblock", &best)
	if best.Height != 2 || best.Hash == "" {
		t.Fatalf("unexpected best block: %+v", best)
	}

	var hash struct {
		Hash string `json:"hash"`
	}
	getJSON(t, httpServer.URL+"/getblockhash/2", &hash)
	if hash.Hash != best.Hash {
		t.Fatalf("hash = %s, want %s", hash.Hash, best.Hash)
	}

	var block struct {
		Height uint32 `json:"height"`
		Tx     []any  `json:"tx"`
	}
	getJSON(t, httpServer.URL+"/getblock/"+hash.Hash, &block)
	if block.Height != 2 || len(block.Tx) != 1 {
		t.Fatalf("unexpected block: %+v", block)
	}
}

func TestListenAndServeShutdown(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.ListenAndServe(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitRawTransactionAndGenerate(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	walletDir := t.TempDir()
	w, err := wallet.Create(wallet.Path(walletDir, params.Name), params)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddKey(params, "recipient"); err != nil {
		t.Fatal(err)
	}

	minerScript, err := address.DecodeAddressScript(params, w.Keys[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}

	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	coinbase := block1.Transactions[0]
	balance := wallet.Balance{UTXOs: []wallet.UTXO{{
		Address: w.Keys[0].Address,
		TxHash:  coinbase.MustTxHash().String(),
		Vout:    0,
		Value:   coinbase.TxOut[0].Value,
		Height:  block1.Header.Height,
	}}}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, chaincfg.Coin, 10_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}
	submitted, err := wallet.SubmitRawTransaction(httpServer.URL, draft.Tx)
	if err != nil {
		t.Fatal(err)
	}
	if !submitted.Accepted || submitted.MempoolSize != 1 {
		t.Fatalf("unexpected submit result: %+v", submitted)
	}

	var mempool struct {
		Size  int      `json:"size"`
		TxIDs []string `json:"txids"`
	}
	getJSON(t, httpServer.URL+"/getrawmempool", &mempool)
	if mempool.Size != 1 || len(mempool.TxIDs) != 1 || mempool.TxIDs[0] != submitted.TxID {
		t.Fatalf("unexpected mempool: %+v", mempool)
	}

	var generated struct {
		Blocks []string `json:"blocks"`
		Height uint32   `json:"height"`
	}
	postJSON(t, httpServer.URL+"/generate", map[string]any{
		"address": w.Keys[0].Address,
		"blocks":  1,
	}, &generated)
	if generated.Height != 2 || len(generated.Blocks) != 1 {
		t.Fatalf("unexpected generate result: %+v", generated)
	}

	var block struct {
		Height uint32 `json:"height"`
		Tx     []any  `json:"tx"`
	}
	getJSON(t, httpServer.URL+"/getblock/2", &block)
	if block.Height != 2 || len(block.Tx) != 2 {
		t.Fatalf("unexpected generated block: %+v", block)
	}
	getJSON(t, httpServer.URL+"/getrawmempool", &mempool)
	if mempool.Size != 0 {
		t.Fatalf("mempool was not cleared: %+v", mempool)
	}
}

func getJSON(t *testing.T, url string, dest any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url string, body any, dest any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}
