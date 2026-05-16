package p2p_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/p2p"
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
