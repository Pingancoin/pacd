package p2p_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/p2p"
	"github.com/Pingancoin/pacd/internal/rpcserver"
	"github.com/Pingancoin/pacd/internal/wallet"
	"github.com/Pingancoin/pacd/internal/wire"
)

func TestNodesHandshake(t *testing.T) {
	params := chaincfg.SimNetParams()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   4,
		BestHeight: func() uint32 { return 7 },
		UserAgent:  "/server-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop")
		}
	})

	waitForListenAddr(t, server)

	client, err := p2p.NewNode(p2p.Config{
		Params:     params,
		MaxPeers:   4,
		BestHeight: func() uint32 { return 3 },
		UserAgent:  "/client-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DialOnce(ctx, server.ListenAddr()); err != nil {
		t.Fatal(err)
	}
	waitForPeers(t, server, 1)
	waitForPeers(t, client, 1)

	serverPeer := server.Peers()[0]
	if !serverPeer.Inbound || serverPeer.BestHeight != 3 || serverPeer.UserAgent != "/client-test/" {
		t.Fatalf("unexpected server peer: %+v", serverPeer)
	}
	clientPeer := client.Peers()[0]
	if clientPeer.Inbound || clientPeer.BestHeight != 7 || clientPeer.UserAgent != "/server-test/" {
		t.Fatalf("unexpected client peer: %+v", clientPeer)
	}
}

func TestHeaderFirstSyncAndBlockRelay(t *testing.T) {
	params := chaincfg.SimNetParams()
	serverChain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 3; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(serverChain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := serverChain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	clientStore := blockstore.New(t.TempDir())
	clientChain := blockchain.New(params)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   4,
		Chain:      serverChain,
		ChainMu:    &sync.Mutex{},
		UserAgent:  "/server-sync-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop")
		}
	})
	waitForListenAddr(t, server)

	client, err := p2p.NewNode(p2p.Config{
		Params:    params,
		MaxPeers:  4,
		Chain:     clientChain,
		Store:     clientStore,
		ChainMu:   &sync.Mutex{},
		UserAgent: "/client-sync-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DialOnce(ctx, server.ListenAddr()); err != nil {
		t.Fatal(err)
	}
	waitForHeight(t, clientChain, 3)
	if clientChain.Tip().MustBlockHash() != serverChain.Tip().MustBlockHash() {
		t.Fatalf("client tip = %s, want %s", clientChain.Tip().MustBlockHash(), serverChain.Tip().MustBlockHash())
	}
	loaded, err := clientStore.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != 3 {
		t.Fatalf("persisted height = %d, want 3", loaded.Height())
	}
}

func TestInventoryRelayFetchesNewBlock(t *testing.T) {
	params := chaincfg.SimNetParams()
	serverChain := blockchain.New(params)
	clientChain := blockchain.New(params)
	clientStore := blockstore.New(t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   4,
		Chain:      serverChain,
		ChainMu:    &sync.Mutex{},
		UserAgent:  "/server-inv-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop")
		}
	})
	waitForListenAddr(t, server)

	client, err := p2p.NewNode(p2p.Config{
		Params:    params,
		MaxPeers:  4,
		Chain:     clientChain,
		Store:     clientStore,
		ChainMu:   &sync.Mutex{},
		UserAgent: "/client-inv-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DialOnce(ctx, server.ListenAddr()); err != nil {
		t.Fatal(err)
	}
	waitForPeers(t, server, 1)
	waitForPeers(t, client, 1)

	blockTime := time.Unix(serverChain.Tip().Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	block, err := mining.MineBlock(serverChain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := serverChain.AddBlock(block); err != nil {
		t.Fatal(err)
	}

	server.RelayBlock(block)
	waitForHeight(t, clientChain, 1)
	if clientChain.Tip().MustBlockHash() != block.MustBlockHash() {
		t.Fatalf("client tip = %s, want %s", clientChain.Tip().MustBlockHash(), block.MustBlockHash())
	}
}

func TestTransactionRelayFetchesNewTx(t *testing.T) {
	params := chaincfg.SimNetParams()
	serverChain := blockchain.New(params)
	clientChain := blockchain.New(params)
	serverMu := &sync.Mutex{}
	clientMu := &sync.Mutex{}

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

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 2; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(serverChain, minerScript, blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := serverChain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	serverRPC := rpcserver.NewWithLock(serverChain, blockstore.New(t.TempDir()), serverMu)
	clientRPC := rpcserver.NewWithLock(clientChain, blockstore.New(t.TempDir()), clientMu)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   4,
		Chain:      serverChain,
		Store:      blockstore.New(t.TempDir()),
		ChainMu:    serverMu,
		UserAgent:  "/server-tx-test/",
		HasTx:      serverRPC.HasTransaction,
		TxByHash:   serverRPC.TransactionByHash,
		AcceptTx:   serverRPC.AcceptTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	serverRPC.SetTransactionAcceptedCallback(server.RelayTransaction)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop")
		}
	})
	waitForListenAddr(t, server)

	client, err := p2p.NewNode(p2p.Config{
		Params:    params,
		MaxPeers:  4,
		Chain:     clientChain,
		Store:     blockstore.New(t.TempDir()),
		ChainMu:   clientMu,
		UserAgent: "/client-tx-test/",
		HasTx:     clientRPC.HasTransaction,
		TxByHash:  clientRPC.TransactionByHash,
		AcceptTx:  clientRPC.AcceptTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.SetBlockConnectedCallback(clientRPC.NotifyBlockConnected)
	if err := client.DialOnce(ctx, server.ListenAddr()); err != nil {
		t.Fatal(err)
	}
	waitForHeight(t, clientChain, 2)

	httpServer := httptest.NewServer(serverRPC.Handler())
	defer httpServer.Close()

	coinbase := serverChain.Blocks()[1].Transactions[0]
	balance := wallet.Balance{UTXOs: []wallet.UTXO{{
		Address:  w.Keys[0].Address,
		TxHash:   coinbase.MustTxHash().String(),
		Vout:     0,
		Value:    coinbase.TxOut[0].Value,
		Height:   1,
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
	if !submitted.Accepted {
		t.Fatalf("transaction was not accepted: %+v", submitted)
	}
	txHash := draft.Tx.MustTxHash()
	waitForTx(t, func() bool {
		return clientRPC.HasTransaction(txHash)
	})

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block, err := mining.MineBlockWithTransactions(serverChain, minerScript, blockTime, []*wire.MsgTx{draft.Tx}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := serverChain.AddBlock(block); err != nil {
		t.Fatal(err)
	}
	serverRPC.NotifyBlockConnected(block)
	server.RelayBlock(block)

	waitForHeight(t, clientChain, 3)
	waitForTxAbsent(t, func() bool {
		return clientRPC.HasTransaction(txHash)
	})
}

func TestAddressDiscoveryConnectsAdditionalPeer(t *testing.T) {
	params := chaincfg.SimNetParams()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seed, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   8,
		UserAgent:  "/seed-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedErrCh := make(chan error, 1)
	go func() {
		seedErrCh <- seed.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-seedErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("seed did not stop")
		}
	})
	waitForListenAddr(t, seed)

	peerB, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		Connect:    []string{seed.ListenAddr()},
		MaxPeers:   8,
		UserAgent:  "/peer-b-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	peerBErrCh := make(chan error, 1)
	go func() {
		peerBErrCh <- peerB.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-peerBErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("peerB did not stop")
		}
	})
	waitForListenAddr(t, peerB)
	waitForPeers(t, seed, 1)
	waitForPeers(t, peerB, 1)
	waitForKnownAddrs(t, seed, 2)

	peerC, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		Connect:    []string{seed.ListenAddr()},
		MaxPeers:   8,
		UserAgent:  "/peer-c-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	peerCErrCh := make(chan error, 1)
	go func() {
		peerCErrCh <- peerC.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-peerCErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("peerC did not stop")
		}
	})
	waitForListenAddr(t, peerC)
	waitForPeers(t, seed, 2)
	waitForPeerAddress(t, peerC, peerB.ListenAddr())
	waitForAddrSource(t, peerC, peerB.ListenAddr(), "verified")
}

func TestAddrBookPersistsDiscoveredAddress(t *testing.T) {
	params := chaincfg.SimNetParams()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrBookPath := t.TempDir() + "/peers.json"
	seed, err := p2p.NewNode(p2p.Config{
		Params:       params,
		ListenAddr:   "127.0.0.1:0",
		MaxPeers:     8,
		AddrBookPath: addrBookPath,
		UserAgent:    "/seed-book-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedErrCh := make(chan error, 1)
	go func() {
		seedErrCh <- seed.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-seedErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("seed did not stop")
		}
	})
	waitForListenAddr(t, seed)

	peer, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		Connect:    []string{seed.ListenAddr()},
		MaxPeers:   8,
		UserAgent:  "/peer-book-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	peerErrCh := make(chan error, 1)
	go func() {
		peerErrCh <- peer.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-peerErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("peer did not stop")
		}
	})
	waitForListenAddr(t, peer)
	waitForKnownAddrs(t, seed, 2)
	waitForAddrSource(t, seed, peer.ListenAddr(), "discovered")
	waitForAddrBookEntry(t, addrBookPath, peer.ListenAddr())
}

func TestAddrBookBootstrapsDiscovery(t *testing.T) {
	params := chaincfg.SimNetParams()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerB, err := p2p.NewNode(p2p.Config{
		Params:     params,
		ListenAddr: "127.0.0.1:0",
		MaxPeers:   8,
		UserAgent:  "/peer-b-book-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	peerBErrCh := make(chan error, 1)
	go func() {
		peerBErrCh <- peerB.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-peerBErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("peerB did not stop")
		}
	})
	waitForListenAddr(t, peerB)

	addrBookPath := t.TempDir() + "/peers.json"
	data, err := json.Marshal([]string{peerB.ListenAddr()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(addrBookPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	node, err := p2p.NewNode(p2p.Config{
		Params:       params,
		ListenAddr:   "127.0.0.1:0",
		MaxPeers:     8,
		AddrBookPath: addrBookPath,
		UserAgent:    "/restored-book-test/",
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeErrCh := make(chan error, 1)
	go func() {
		nodeErrCh <- node.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-nodeErrCh:
		case <-time.After(2 * time.Second):
			t.Fatal("node did not stop")
		}
	})
	waitForListenAddr(t, node)
	waitForPeerAddress(t, node, peerB.ListenAddr())
	waitForAddrSource(t, node, peerB.ListenAddr(), "verified")
}

func waitForListenAddr(t *testing.T, node *p2p.Node) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if node.ListenAddr() != "127.0.0.1:0" && node.ListenAddr() != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("node did not start listening")
}

func waitForPeers(t *testing.T, node *p2p.Node, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if node.PeerCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer count = %d, want %d", node.PeerCount(), want)
}

func waitForHeight(t *testing.T, chain *blockchain.Chain, want uint32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if chain.Height() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("height = %d, want %d", chain.Height(), want)
}

func waitForTx(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transaction was not relayed")
}

func waitForTxAbsent(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transaction was not pruned")
}

func waitForKnownAddrs(t *testing.T, node *p2p.Node, wantAtLeast int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if node.KnownAddressCount() >= wantAtLeast {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("known addr count = %d, want at least %d", node.KnownAddressCount(), wantAtLeast)
}

func waitForPeerAddress(t *testing.T, node *p2p.Node, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, peer := range node.Peers() {
			if peer.Address == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer %s was not discovered; peers=%+v", want, node.Peers())
}

func waitForAddrBookEntry(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var addrs []struct {
				Address string `json:"address"`
			}
			if err := json.Unmarshal(data, &addrs); err == nil {
				for _, addr := range addrs {
					if addr.Address == want {
						return
					}
				}
			} else {
				var legacy []string
				if err := json.Unmarshal(data, &legacy); err == nil {
					for _, addr := range legacy {
						if addr == want {
							return
						}
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("addrbook %s did not contain %s", path, want)
}

func waitForAddrSource(t *testing.T, node *p2p.Node, wantAddr string, wantSource string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range node.AddrBook() {
			if entry.Address == wantAddr && entry.Source == wantSource {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("addr %s did not reach source %s; addrbook=%+v", wantAddr, wantSource, node.AddrBook())
}
