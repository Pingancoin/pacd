package blockchain_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
)

func TestMineAndValidateSimnetBlock(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)

	block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block); err != nil {
		t.Fatal(err)
	}
	if chain.Height() != 1 {
		t.Fatalf("height = %d, want 1", chain.Height())
	}
}

func TestMineAndValidateOneHundredSimnetBlocks(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)

	const blocks = 100
	for i := 0; i < blocks; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	if chain.Height() != blocks {
		t.Fatalf("height = %d, want %d", chain.Height(), blocks)
	}
	wantSupply := params.BaseSubsidy * blocks
	if chain.TotalSubsidy() != wantSupply {
		t.Fatalf("total subsidy = %d, want %d", chain.TotalSubsidy(), wantSupply)
	}
}
