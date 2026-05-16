package blockstore_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
)

func TestStoreLoadAppend(t *testing.T) {
	params := chaincfg.SimNetParams()
	store := blockstore.New(t.TempDir())

	chain, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 3; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
		if err := store.Append(block); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != chain.Height() {
		t.Fatalf("loaded height = %d, want %d", loaded.Height(), chain.Height())
	}
	if loaded.Tip().MustBlockHash() != chain.Tip().MustBlockHash() {
		t.Fatalf("loaded tip hash mismatch")
	}
	if loaded.TotalSubsidy() != chain.TotalSubsidy() {
		t.Fatalf("loaded subsidy = %d, want %d", loaded.TotalSubsidy(), chain.TotalSubsidy())
	}
}
