package rpcserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/rpcserver"
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
