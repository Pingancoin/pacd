package blockchain_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wire"
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
	if chain.UTXOCount() != 2 {
		t.Fatalf("utxo count = %d, want 2", chain.UTXOCount())
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

func TestRegularTransactionUpdatesUTXOSetAndPaysFee(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}

	coinbaseHash := block1.Transactions[0].MustTxHash()
	minerOutpoint := wire.OutPoint{Hash: coinbaseHash, Index: 0}
	minerOut, ok := chain.LookupUTXO(minerOutpoint)
	if !ok {
		t.Fatal("missing miner coinbase utxo")
	}

	const fee = int64(10_000)
	spend := &wire.MsgTx{
		Version: 1,
		TxIn: []*wire.TxIn{{
			PreviousOutPoint: minerOutpoint,
			SignatureScript:  []byte("simnet-signature-placeholder"),
			Sequence:         wire.MaxUint32,
		}},
		TxOut: []*wire.TxOut{{
			Value:    minerOut.Value - fee,
			PkScript: []byte("SsimRecipient"),
		}},
	}

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block2, err := mining.MineBlockWithTransactions(chain, []byte("SsimMiner"), blockTime, []*wire.MsgTx{spend}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block2); err != nil {
		t.Fatal(err)
	}

	if _, ok := chain.LookupUTXO(minerOutpoint); ok {
		t.Fatal("spent miner outpoint is still in utxo set")
	}
	spendOutpoint := wire.OutPoint{Hash: spend.MustTxHash(), Index: 0}
	if _, ok := chain.LookupUTXO(spendOutpoint); !ok {
		t.Fatal("missing regular transaction output in utxo set")
	}

	minerSubsidy, _ := chaincfgSplit(t, params, block2.Header.Height)
	if got := block2.Transactions[0].TxOut[0].Value; got != minerSubsidy+fee {
		t.Fatalf("coinbase miner reward = %d, want %d", got, minerSubsidy+fee)
	}
}

func TestRejectDoubleSpendInBlock(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}

	coinbaseHash := block1.Transactions[0].MustTxHash()
	minerOutpoint := wire.OutPoint{Hash: coinbaseHash, Index: 0}
	minerOut, ok := chain.LookupUTXO(minerOutpoint)
	if !ok {
		t.Fatal("missing miner coinbase utxo")
	}

	spend := func(script string) *wire.MsgTx {
		return &wire.MsgTx{
			Version: 1,
			TxIn: []*wire.TxIn{{
				PreviousOutPoint: minerOutpoint,
				SignatureScript:  []byte("simnet-signature-placeholder"),
				Sequence:         wire.MaxUint32,
			}},
			TxOut: []*wire.TxOut{{
				Value:    minerOut.Value - 1,
				PkScript: []byte(script),
			}},
		}
	}

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	if _, err := mining.MineBlockWithTransactions(chain, []byte("SsimMiner"), blockTime, []*wire.MsgTx{
		spend("SsimRecipient1"),
		spend("SsimRecipient2"),
	}, 0); err == nil {
		t.Fatal("expected double spend to be rejected")
	}
}

func chaincfgSplit(t *testing.T, params *chaincfg.Params, height uint32) (int64, int64) {
	t.Helper()
	miner := params.BaseSubsidy * params.MinerRewardPercent / 100
	project := params.BaseSubsidy - miner
	if height >= uint32(params.ReductionInterval) {
		t.Fatal("test helper only supports pre-reduction heights")
	}
	return miner, project
}
