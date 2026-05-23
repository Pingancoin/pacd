package rpcserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/rpcserver"
	"github.com/Pingancoin/pacd/internal/wallet"
	"github.com/Pingancoin/pacd/internal/wire"
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

func TestBearerAuth(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	server := rpcserver.NewWithOptions(chain, blockstore.New(t.TempDir()), nil, rpcserver.Options{AuthToken: "secret-token"})
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL + "/getblockcount")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %s, want 401", resp.Status)
	}

	req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/getblockcount", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %s, want 200", resp.Status)
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
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block2, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block2); err != nil {
		t.Fatal(err)
	}

	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	coinbase := block1.Transactions[0]
	balance := wallet.Balance{UTXOs: []wallet.UTXO{{
		Address:  w.Keys[0].Address,
		TxHash:   coinbase.MustTxHash().String(),
		Vout:     0,
		Value:    coinbase.TxOut[0].Value,
		Height:   block1.Header.Height,
		Coinbase: true,
		Mature:   true,
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
	var pendingTx struct {
		Hash    string `json:"hash"`
		Pending bool   `json:"pending"`
		Hex     string `json:"hex"`
	}
	getJSON(t, httpServer.URL+"/getrawtransaction/"+submitted.TxID, &pendingTx)
	if pendingTx.Hash != submitted.TxID || !pendingTx.Pending || pendingTx.Hex == "" {
		t.Fatalf("unexpected pending tx: %+v", pendingTx)
	}

	var generated struct {
		Blocks []string `json:"blocks"`
		Height uint32   `json:"height"`
	}
	postJSON(t, httpServer.URL+"/generate", map[string]any{
		"address": w.Keys[0].Address,
		"blocks":  1,
	}, &generated)
	if generated.Height != 3 || len(generated.Blocks) != 1 {
		t.Fatalf("unexpected generate result: %+v", generated)
	}

	var block struct {
		Height uint32 `json:"height"`
		Tx     []any  `json:"tx"`
	}
	getJSON(t, httpServer.URL+"/getblock/3", &block)
	if block.Height != 3 || len(block.Tx) != 2 {
		t.Fatalf("unexpected generated block: %+v", block)
	}
	var confirmedTx struct {
		Hash    string `json:"hash"`
		Height  uint32 `json:"height"`
		Pending bool   `json:"pending"`
	}
	getJSON(t, httpServer.URL+"/getrawtransaction/"+submitted.TxID, &confirmedTx)
	if confirmedTx.Hash != submitted.TxID || confirmedTx.Pending || confirmedTx.Height != 3 {
		t.Fatalf("unexpected confirmed tx: %+v", confirmedTx)
	}
	var addressUTXOs struct {
		Address string `json:"address"`
		UTXOs   []struct {
			TxHash string `json:"tx_hash"`
			Value  int64  `json:"value"`
		} `json:"utxos"`
	}
	getJSON(t, httpServer.URL+"/getaddressutxos/"+w.Keys[1].Address, &addressUTXOs)
	if addressUTXOs.Address != w.Keys[1].Address || len(addressUTXOs.UTXOs) != 1 || addressUTXOs.UTXOs[0].TxHash != submitted.TxID {
		t.Fatalf("unexpected address utxos: %+v", addressUTXOs)
	}
	getJSON(t, httpServer.URL+"/getrawmempool", &mempool)
	if mempool.Size != 0 {
		t.Fatalf("mempool was not cleared: %+v", mempool)
	}

	history, err := wallet.ScanHistory(params, w, httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("history entries = %d, want 4: %+v", len(history), history)
	}
	if got := history[len(history)-1]; got.TxHash != submitted.TxID || got.Pending || got.Sent == 0 || got.Received == 0 {
		t.Fatalf("unexpected wallet history entry: %+v", got)
	}
}

func TestGetBlockTemplateAndSubmitBlock(t *testing.T) {
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
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block2, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block2); err != nil {
		t.Fatal(err)
	}

	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	coinbase := block1.Transactions[0]
	balance := wallet.Balance{UTXOs: []wallet.UTXO{{
		Address:  w.Keys[0].Address,
		TxHash:   coinbase.MustTxHash().String(),
		Vout:     0,
		Value:    coinbase.TxOut[0].Value,
		Height:   block1.Header.Height,
		Coinbase: true,
		Mature:   true,
	}}}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, chaincfg.Coin, 10_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := wallet.SubmitRawTransaction(httpServer.URL, draft.Tx); err != nil {
		t.Fatal(err)
	}

	var template struct {
		Height         uint32   `json:"height"`
		MempoolSize    int      `json:"mempoolsize"`
		TotalFees      int64    `json:"totalfees"`
		CoinbaseTxID   string   `json:"coinbasetxid"`
		HeaderHex      string   `json:"headerhex"`
		BlockHex       string   `json:"blockhex"`
		TransactionIDs []string `json:"transactionids"`
	}
	postJSON(t, httpServer.URL+"/getblocktemplate", map[string]any{
		"address": w.Keys[0].Address,
	}, &template)
	if template.Height != 3 || template.MempoolSize != 1 || template.TotalFees <= 0 || template.CoinbaseTxID == "" || template.HeaderHex == "" || template.BlockHex == "" || len(template.TransactionIDs) != 1 {
		t.Fatalf("unexpected template: %+v", template)
	}

	blockBytes, err := hex.DecodeString(template.BlockHex)
	if err != nil {
		t.Fatal(err)
	}
	block, err := wire.DeserializeBlock(blockBytes)
	if err != nil {
		t.Fatal(err)
	}
	solveBlock(t, block, params)

	var submitted struct {
		Accepted bool   `json:"accepted"`
		Hash     string `json:"hash"`
		Height   uint32 `json:"height"`
	}
	postJSON(t, httpServer.URL+"/submitblock", map[string]any{
		"blockhex": hex.EncodeToString(mustBlockBytes(t, block)),
	}, &submitted)
	if !submitted.Accepted || submitted.Height != 3 || submitted.Hash == "" {
		t.Fatalf("unexpected submit block result: %+v", submitted)
	}

	var best struct {
		Height uint32 `json:"height"`
		Hash   string `json:"hash"`
	}
	getJSON(t, httpServer.URL+"/getbestblock", &best)
	if best.Height != 3 || best.Hash != submitted.Hash {
		t.Fatalf("unexpected best block: %+v", best)
	}
}

func TestMiningRPCWaitsForMiningStart(t *testing.T) {
	params := chaincfg.SimNetParams()
	params.MiningStartTime = time.Now().UTC().Add(time.Hour).Unix()
	chain := blockchain.New(params)
	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	resp := postJSONStatus(t, httpServer.URL+"/getblocktemplate", map[string]any{
		"address": "Snot-used-before-launch",
	})
	if resp != http.StatusServiceUnavailable {
		t.Fatalf("getblocktemplate status = %d, want %d", resp, http.StatusServiceUnavailable)
	}
	resp = postJSONStatus(t, httpServer.URL+"/submitblock", map[string]any{
		"blockhex": "00",
	})
	if resp != http.StatusServiceUnavailable {
		t.Fatalf("submitblock status = %d, want %d", resp, http.StatusServiceUnavailable)
	}
}

func TestNotifyChainReorganizedRestoresValidDisconnectedTransactions(t *testing.T) {
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
	if err := w.AddKey(params, "change"); err != nil {
		t.Fatal(err)
	}

	minerScript, err := address.DecodeAddressScript(params, w.Keys[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block2, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block2); err != nil {
		t.Fatal(err)
	}

	coinbase := block1.Transactions[0]
	balance := wallet.Balance{UTXOs: []wallet.UTXO{{
		Address:  w.Keys[0].Address,
		TxHash:   coinbase.MustTxHash().String(),
		Vout:     0,
		Value:    coinbase.TxOut[0].Value,
		Height:   block1.Header.Height,
		Coinbase: true,
		Mature:   true,
	}}}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, chaincfg.Coin, 10_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	oldBlock3, err := mining.MineBlockWithTransactions(chain, minerScript, blockTime, []*wire.MsgTx{draft.Tx}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(oldBlock3); err != nil {
		t.Fatal(err)
	}

	sideChain := blockchain.New(params)
	if err := sideChain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}
	if err := sideChain.AddBlock(block2); err != nil {
		t.Fatal(err)
	}
	sideBlocks := make([]*wire.MsgBlock, 0, 2)
	sideTime := time.Unix(block2.Header.Timestamp, 0)
	for i := 0; i < 2; i++ {
		sideTime = sideTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(sideChain, []byte("SsimSide"), sideTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		sideBlocks = append(sideBlocks, block)
		if err := sideChain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	ok, err := chain.Reorganize(sideBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("side chain did not reorganize")
	}
	server.NotifyChainReorganized([]*wire.MsgBlock{oldBlock3}, sideBlocks)
	if !server.HasTransaction(draft.Tx.MustTxHash()) {
		t.Fatal("valid disconnected transaction was not restored to mempool")
	}
}

func TestNetworkInfoAndPeerInfo(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	server := rpcserver.New(chain, blockstore.New(t.TempDir()))
	server.SetPeerCallbacks(
		func() []rpcserver.PeerSnapshot {
			return []rpcserver.PeerSnapshot{{
				Address:    "127.0.0.1:39508",
				Inbound:    false,
				BestHeight: 12,
				UserAgent:  "/pacd:test/",
			}}
		},
		func() int { return 1 },
		func() int { return 5 },
	)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var networkInfo struct {
		Network        string `json:"network"`
		BestHeight     uint32 `json:"bestheight"`
		PeerCount      int    `json:"peercount"`
		KnownAddrCount int    `json:"knownaddrcount"`
		MempoolSize    int    `json:"mempoolsize"`
	}
	getJSON(t, httpServer.URL+"/getnetworkinfo", &networkInfo)
	if networkInfo.Network != params.Name || networkInfo.BestHeight != 0 || networkInfo.PeerCount != 1 || networkInfo.KnownAddrCount != 5 || networkInfo.MempoolSize != 0 {
		t.Fatalf("unexpected network info: %+v", networkInfo)
	}

	var peerInfo struct {
		Count int                      `json:"count"`
		Peers []rpcserver.PeerSnapshot `json:"peers"`
	}
	getJSON(t, httpServer.URL+"/getpeerinfo", &peerInfo)
	if peerInfo.Count != 1 || len(peerInfo.Peers) != 1 || peerInfo.Peers[0].Address != "127.0.0.1:39508" || peerInfo.Peers[0].BestHeight != 12 {
		t.Fatalf("unexpected peer info: %+v", peerInfo)
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

func postJSONStatus(t *testing.T, url string, body any) int {
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
	return resp.StatusCode
}

func solveBlock(t *testing.T, block *wire.MsgBlock, params *chaincfg.Params) {
	t.Helper()
	for nonce := uint32(0); nonce <= math.MaxUint32; nonce++ {
		block.Header.Nonce = nonce
		if consensus.CheckProofOfWork(&block.Header, params) == nil {
			return
		}
		if nonce == math.MaxUint32 {
			break
		}
	}
	t.Fatal("failed to solve template block")
}

func mustBlockBytes(t *testing.T, block *wire.MsgBlock) []byte {
	t.Helper()
	serialized, err := block.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}
