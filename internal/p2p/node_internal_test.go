package p2p

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wire"
)

func TestBanScoreAppliesByHost(t *testing.T) {
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		MaxPeers: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	node.addBanScore("127.0.0.1:10000", banThreshold, "test")
	if node.reservePeer("127.0.0.1:10001", false, false) {
		t.Fatal("peer from banned host was accepted with a different port")
	}
	if !node.reservePeer("127.0.0.2:10000", false, false) {
		t.Fatal("peer from a different host was unexpectedly rejected")
	}
}

func TestReservePeerDeduplicatesByHost(t *testing.T) {
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		MaxPeers: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !node.reservePeer("203.0.113.1:10000", false, false) {
		t.Fatal("first peer was rejected")
	}
	if node.reservePeer("203.0.113.1:10001", false, false) {
		t.Fatal("second peer from same host was accepted")
	}
	if !node.reservePeer("203.0.113.2:10000", false, false) {
		t.Fatal("peer from different host was rejected")
	}
}

func TestReservePeerLimitsPendingPeersSeparately(t *testing.T) {
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		MaxPeers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !node.reservePeer("203.0.113.1:10000", false, false) {
		t.Fatal("first pending peer was rejected")
	}
	if !node.reservePeer("203.0.113.2:10000", false, false) {
		t.Fatal("second pending peer was rejected")
	}
	if node.reservePeer("203.0.113.3:10000", false, false) {
		t.Fatal("pending peer limit was not enforced")
	}
}

func TestReservePeerAllowsStaticPeerWhenPendingIsFull(t *testing.T) {
	staticAddr := "203.0.113.10:9508"
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		Connect:  []string{staticAddr},
		MaxPeers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !node.reservePeer("203.0.113.1:10000", false, false) {
		t.Fatal("first pending peer was rejected")
	}
	if !node.reservePeer("203.0.113.2:10000", false, false) {
		t.Fatal("second pending peer was rejected")
	}
	if !node.reservePeer(staticAddr, false, false) {
		t.Fatal("static peer was blocked by pending reservations")
	}
}

func TestReservePeerStaticDialReplacesPendingSameHost(t *testing.T) {
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		MaxPeers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !node.reservePeer("203.0.113.10:10000", false, false) {
		t.Fatal("pending peer was rejected")
	}
	if !node.reservePeer("203.0.113.10:9508", false, true) {
		t.Fatal("static dial was blocked by pending same-host reservation")
	}
	if _, ok := node.peers["203.0.113.10:10000"]; ok {
		t.Fatal("pending same-host reservation was not replaced")
	}
}

func TestReservePeerStaticDialReplacesPendingSameAddress(t *testing.T) {
	addr := "203.0.113.10:9508"
	node, err := NewNode(Config{
		Params:   chaincfg.SimNetParams(),
		MaxPeers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !node.reservePeer(addr, false, false) {
		t.Fatal("pending peer was rejected")
	}
	if !node.reservePeer(addr, false, true) {
		t.Fatal("static dial was blocked by pending same-address reservation")
	}
	if got := len(node.peers); got != 1 {
		t.Fatalf("expected one replacement reservation, got %d", got)
	}
}

func TestInvalidOrphanIsRejectedBeforeCache(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	node, err := NewNode(Config{
		Params:  params,
		Chain:   chain,
		ChainMu: &sync.Mutex{},
	})
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	block.Header.PrevBlock[0] ^= 0xff
	block.Transactions[0].TxOut[0].Value++

	connected, connectedBlocks, _, err := node.connectBlock("", block)
	if err == nil {
		t.Fatal("invalid orphan was accepted")
	}
	if connected || len(connectedBlocks) != 0 {
		t.Fatalf("unexpected connected orphan result: connected=%t blocks=%d", connected, len(connectedBlocks))
	}
	if len(node.orphans) != 0 {
		t.Fatalf("invalid orphan was cached; orphan count=%d", len(node.orphans))
	}
}

func TestValidOrphanConnectsAfterParent(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	node, err := NewNode(Config{
		Params:  params,
		Chain:   chain,
		ChainMu: &sync.Mutex{},
	})
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}

	sideChain := blockchain.New(params)
	if err := sideChain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block2, err := mining.MineBlock(sideChain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}

	connected, connectedBlocks, _, err := node.connectBlock("", block2)
	if err != nil {
		t.Fatal(err)
	}
	if connected || len(connectedBlocks) != 0 {
		t.Fatalf("orphan connected before parent: connected=%t blocks=%d", connected, len(connectedBlocks))
	}
	if len(node.orphans) != 1 {
		t.Fatalf("orphan count=%d, want 1", len(node.orphans))
	}

	connected, connectedBlocks, _, err = node.connectBlock("", block1)
	if err != nil {
		t.Fatal(err)
	}
	if !connected || len(connectedBlocks) != 2 {
		t.Fatalf("parent did not connect orphan chain: connected=%t blocks=%d", connected, len(connectedBlocks))
	}
	if chain.Height() != 2 || chain.Tip().MustBlockHash() != block2.MustBlockHash() {
		t.Fatalf("tip height/hash mismatch: height=%d hash=%s", chain.Height(), chain.Tip().MustBlockHash())
	}
	if len(node.orphans) != 0 {
		t.Fatalf("orphan cache not cleared; count=%d", len(node.orphans))
	}
}

func TestHandleHeadersCapsGetBlocksRequest(t *testing.T) {
	params := chaincfg.SimNetParams()
	localChain := blockchain.New(params)
	remoteChain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	headers := make([]wire.BlockHeader, 0, MaxBlocksPerRequest+1)
	for i := 0; i < MaxBlocksPerRequest+1; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(remoteChain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		headers = append(headers, block.Header)
		if err := remoteChain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := (Headers{Headers: headers}).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	node, err := NewNode(Config{
		Params:  params,
		Chain:   localChain,
		ChainMu: &sync.Mutex{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	addr := "127.0.0.1:10000"
	node.updatePeer(addr, PeerInfo{Address: addr}, server, "")

	errCh := make(chan error, 1)
	go func() {
		errCh <- node.handleHeaders(addr, payload)
	}()
	msg, err := ReadMessage(client, params.NetworkMagic)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if msg.Command != CommandGetBlocks {
		t.Fatalf("command = %s, want %s", msg.Command, CommandGetBlocks)
	}
	req, err := DeserializeGetBlocks(msg.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Hashes) != MaxBlocksPerRequest {
		t.Fatalf("requested blocks = %d, want %d", len(req.Hashes), MaxBlocksPerRequest)
	}
}

func TestStaleConflictingBlockIsIgnored(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	node, err := NewNode(Config{
		Params:  params,
		Chain:   chain,
		ChainMu: &sync.Mutex{},
	})
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	blockA, err := mining.MineBlock(chain, []byte("SsimMinerA"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(blockA); err != nil {
		t.Fatal(err)
	}

	sideChain := blockchain.New(params)
	blockB, err := mining.MineBlock(sideChain, []byte("SsimMinerB"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if blockB.MustBlockHash() == blockA.MustBlockHash() {
		t.Fatal("test did not create a conflicting block")
	}

	connected, connectedBlocks, _, err := node.connectBlock("", blockB)
	if err != nil {
		t.Fatalf("stale conflicting block returned error: %v", err)
	}
	if connected || len(connectedBlocks) != 0 {
		t.Fatalf("stale conflicting block connected: connected=%t blocks=%d", connected, len(connectedBlocks))
	}
	if chain.Tip().MustBlockHash() != blockA.MustBlockHash() {
		t.Fatalf("tip changed after stale conflict: %s", chain.Tip().MustBlockHash())
	}
}

func TestLongerSideChainReorganizesAndPersists(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	store := blockstore.New(t.TempDir())
	node, err := NewNode(Config{
		Params:  params,
		Chain:   chain,
		Store:   store,
		ChainMu: &sync.Mutex{},
	})
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 2; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMain"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	mainTip := chain.Tip().MustBlockHash()

	sideChain := blockchain.New(params)
	sideTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	sideBlocks := make([]*wire.MsgBlock, 0, 3)
	for i := 0; i < 3; i++ {
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

	for i, block := range sideBlocks {
		connected, connectedBlocks, disconnectedBlocks, err := node.connectBlock("", block)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && connected {
			t.Fatalf("side block %d reorganized before becoming longer", i)
		}
		if i == 2 {
			if !connected || len(connectedBlocks) != 3 {
				t.Fatalf("longer side chain did not reorganize: connected=%t blocks=%d", connected, len(connectedBlocks))
			}
			if len(disconnectedBlocks) != 2 {
				t.Fatalf("disconnected blocks=%d, want 2", len(disconnectedBlocks))
			}
		}
	}
	if chain.Height() != 3 || chain.Tip().MustBlockHash() != sideBlocks[2].MustBlockHash() {
		t.Fatalf("unexpected reorg tip height=%d hash=%s", chain.Height(), chain.Tip().MustBlockHash())
	}
	if chain.Tip().MustBlockHash() == mainTip {
		t.Fatal("main tip did not change after reorg")
	}
	if len(node.sideBlocks) != 0 {
		t.Fatalf("side cache not pruned: %d", len(node.sideBlocks))
	}
	loaded, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != 3 || loaded.Tip().MustBlockHash() != sideBlocks[2].MustBlockHash() {
		t.Fatalf("persisted reorg height=%d hash=%s", loaded.Height(), loaded.Tip().MustBlockHash())
	}
}
