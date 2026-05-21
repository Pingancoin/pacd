package p2p

import (
	"sync"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
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
	if node.reservePeer("127.0.0.1:10001") {
		t.Fatal("peer from banned host was accepted with a different port")
	}
	if !node.reservePeer("127.0.0.2:10000") {
		t.Fatal("peer from a different host was unexpectedly rejected")
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

	connected, connectedBlocks, err := node.connectBlock(block)
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

	connected, connectedBlocks, err := node.connectBlock(block2)
	if err != nil {
		t.Fatal(err)
	}
	if connected || len(connectedBlocks) != 0 {
		t.Fatalf("orphan connected before parent: connected=%t blocks=%d", connected, len(connectedBlocks))
	}
	if len(node.orphans) != 1 {
		t.Fatalf("orphan count=%d, want 1", len(node.orphans))
	}

	connected, connectedBlocks, err = node.connectBlock(block1)
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
