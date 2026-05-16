package p2p_test

import (
	"context"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/chaincfg"
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
