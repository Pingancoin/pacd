package blockchain_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wallet"
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
	w := testWallet(t, params)
	minerScript := testAddressScript(t, params, w.Keys[0].Address)
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

	const fee = int64(10_000)
	if err := w.AddKey(params, "recipient"); err != nil {
		t.Fatal(err)
	}
	balance := wallet.Balance{
		UTXOs: []wallet.UTXO{{
			Address: w.Keys[0].Address,
			TxHash:  block1.Transactions[0].MustTxHash().String(),
			Vout:    0,
			Value:   block1.Transactions[0].TxOut[0].Value,
			Height:  block1.Header.Height,
		}},
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, balance.UTXOs[0].Value-fee, fee, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}
	spend := draft.Tx

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block3, err := mining.MineBlockWithTransactions(chain, []byte("SsimMiner"), blockTime, []*wire.MsgTx{spend}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block3); err != nil {
		t.Fatal(err)
	}

	minerOutpoint := wire.OutPoint{Hash: block1.Transactions[0].MustTxHash(), Index: 0}
	if _, ok := chain.LookupUTXO(minerOutpoint); ok {
		t.Fatal("spent miner outpoint is still in utxo set")
	}
	spendOutpoint := wire.OutPoint{Hash: spend.MustTxHash(), Index: 0}
	if _, ok := chain.LookupUTXO(spendOutpoint); !ok {
		t.Fatal("missing regular transaction output in utxo set")
	}

	minerSubsidy, _ := chaincfgSplit(t, params, block3.Header.Height)
	if got := block3.Transactions[0].TxOut[0].Value; got != minerSubsidy+fee {
		t.Fatalf("coinbase miner reward = %d, want %d", got, minerSubsidy+fee)
	}
}

func TestRejectImmatureCoinbaseSpend(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	w := testWallet(t, params)
	minerScript := testAddressScript(t, params, w.Keys[0].Address)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	block1, err := mining.MineBlock(chain, minerScript, blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AddBlock(block1); err != nil {
		t.Fatal(err)
	}

	if err := w.AddKey(params, "recipient"); err != nil {
		t.Fatal(err)
	}
	balance := wallet.Balance{
		UTXOs: []wallet.UTXO{{
			Address:  w.Keys[0].Address,
			TxHash:   block1.Transactions[0].MustTxHash().String(),
			Vout:     0,
			Value:    block1.Transactions[0].TxOut[0].Value,
			Height:   block1.Header.Height,
			Coinbase: true,
			Mature:   true,
		}},
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, balance.UTXOs[0].Value-1, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		t.Fatal(err)
	}

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	if _, err := mining.MineBlockWithTransactions(chain, []byte("SsimMiner"), blockTime, []*wire.MsgTx{draft.Tx}, 0); err == nil {
		t.Fatal("expected immature coinbase spend to be rejected")
	}
}

func TestRejectDoubleSpendInBlock(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	w := testWallet(t, params)
	minerScript := testAddressScript(t, params, w.Keys[0].Address)
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

	if err := w.AddKey(params, "recipient"); err != nil {
		t.Fatal(err)
	}
	balance := wallet.Balance{
		UTXOs: []wallet.UTXO{{
			Address: w.Keys[0].Address,
			TxHash:  block1.Transactions[0].MustTxHash().String(),
			Vout:    0,
			Value:   block1.Transactions[0].TxOut[0].Value,
			Height:  block1.Header.Height,
		}},
	}
	spend := func() *wire.MsgTx {
		draft, err := wallet.BuildDraftTx(params, w, balance, w.Keys[1].Address, balance.UTXOs[0].Value-1, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := wallet.SignDraftTx(params, w, draft); err != nil {
			t.Fatal(err)
		}
		return draft.Tx
	}

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	if _, err := mining.MineBlockWithTransactions(chain, []byte("SsimMiner"), blockTime, []*wire.MsgTx{
		spend(),
		spend(),
	}, 0); err == nil {
		t.Fatal("expected double spend to be rejected")
	}
}

func TestReorganizeToLongerFork(t *testing.T) {
	params := chaincfg.SimNetParams()
	mainChain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)

	blockTime = blockTime.Add(params.TargetTimePerBlock)
	main1, err := mining.MineBlock(mainChain, []byte("SsimMain1"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := mainChain.AddBlock(main1); err != nil {
		t.Fatal(err)
	}
	blockTime = blockTime.Add(params.TargetTimePerBlock)
	main2, err := mining.MineBlock(mainChain, []byte("SsimMain2"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := mainChain.AddBlock(main2); err != nil {
		t.Fatal(err)
	}

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

	ok, err := mainChain.Reorganize(sideBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("longer fork did not reorganize")
	}
	if mainChain.Height() != 3 || mainChain.Tip().MustBlockHash() != sideBlocks[2].MustBlockHash() {
		t.Fatalf("unexpected reorganized tip height=%d hash=%s", mainChain.Height(), mainChain.Tip().MustBlockHash())
	}
	if _, ok := mainChain.BlockByHash(main2.MustBlockHash()); ok {
		t.Fatal("old main-chain block is still active after reorg")
	}
}

func TestReorganizeRejectsShorterFork(t *testing.T) {
	params := chaincfg.SimNetParams()
	mainChain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 3; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(mainChain, []byte("SsimMain"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := mainChain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}

	sideChain := blockchain.New(params)
	sideTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	var sideBlocks []*wire.MsgBlock
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

	ok, err := mainChain.Reorganize(sideBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("shorter fork reorganized")
	}
	if mainChain.Height() != 3 {
		t.Fatalf("height changed to %d", mainChain.Height())
	}
}

func testWallet(t *testing.T, params *chaincfg.Params) *wallet.Wallet {
	t.Helper()
	w, err := wallet.Create(wallet.Path(t.TempDir(), params.Name), params)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func testAddressScript(t *testing.T, params *chaincfg.Params, addr string) []byte {
	t.Helper()
	script, err := address.DecodeAddressScript(params, addr)
	if err != nil {
		t.Fatal(err)
	}
	return script
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
