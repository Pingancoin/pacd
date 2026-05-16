package blockchain

import (
	"bytes"
	"fmt"
	"math"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/txscript"
	"github.com/Pingancoin/pacd/internal/wire"
)

type Chain struct {
	params *chaincfg.Params
	blocks []*wire.MsgBlock
	utxos  map[wire.OutPoint]*UTXOEntry
}

type UTXOEntry struct {
	TxOut       wire.TxOut
	BlockHeight uint32
	Coinbase    bool
}

func New(params *chaincfg.Params) *Chain {
	return &Chain{
		params: params,
		blocks: []*wire.MsgBlock{params.GenesisBlock},
		utxos:  make(map[wire.OutPoint]*UTXOEntry),
	}
}

func (c *Chain) Params() *chaincfg.Params {
	return c.params
}

func (c *Chain) Tip() *wire.MsgBlock {
	return c.blocks[len(c.blocks)-1]
}

func (c *Chain) Height() uint32 {
	return c.Tip().Header.Height
}

func (c *Chain) Blocks() []*wire.MsgBlock {
	return append([]*wire.MsgBlock(nil), c.blocks...)
}

func (c *Chain) BlockByHeight(height uint32) (*wire.MsgBlock, bool) {
	if int(height) >= len(c.blocks) {
		return nil, false
	}
	return c.blocks[height], true
}

func (c *Chain) BlockByHash(hash wire.Hash) (*wire.MsgBlock, bool) {
	for _, block := range c.blocks {
		if block.MustBlockHash() == hash {
			return block, true
		}
	}
	return nil, false
}

func (c *Chain) TotalSubsidy() int64 {
	var total int64
	for _, block := range c.blocks {
		total += consensus.CalcBlockSubsidy(int64(block.Header.Height), c.params)
	}
	return total
}

func (c *Chain) UTXOCount() int {
	return len(c.utxos)
}

func (c *Chain) LookupUTXO(outpoint wire.OutPoint) (wire.TxOut, bool) {
	txOut, ok := c.utxos[outpoint]
	if !ok {
		return wire.TxOut{}, false
	}
	return cloneTxOut(&txOut.TxOut), true
}

func (c *Chain) ExpectedBits(nextHeight uint32) uint32 {
	return consensus.CalcASERTNextBits(
		c.params.GenesisBlock.Header.Bits,
		c.params.GenesisBlock.Header.Timestamp,
		c.Tip().Header.Timestamp,
		int64(nextHeight),
		c.params,
	)
}

func (c *Chain) AddBlock(block *wire.MsgBlock) error {
	nextUTXOs, err := c.validateBlock(block)
	if err != nil {
		return err
	}
	c.blocks = append(c.blocks, block)
	c.utxos = nextUTXOs
	return nil
}

func (c *Chain) ValidateBlock(block *wire.MsgBlock) error {
	_, err := c.validateBlock(block)
	return err
}

func (c *Chain) CalcFees(txs []*wire.MsgTx) (int64, error) {
	fees, _, err := c.validateRegularTransactions(txs, cloneUTXOSet(c.utxos), c.Height()+1)
	return fees, err
}

func (c *Chain) validateBlock(block *wire.MsgBlock) (map[wire.OutPoint]*UTXOEntry, error) {
	if len(block.Transactions) == 0 {
		return nil, fmt.Errorf("block has no transactions")
	}

	expectedHeight := c.Height() + 1
	if block.Header.Height != expectedHeight {
		return nil, fmt.Errorf("height %d does not extend tip %d", block.Header.Height, c.Height())
	}

	tipHash := c.Tip().MustBlockHash()
	if block.Header.PrevBlock != tipHash {
		return nil, fmt.Errorf("previous block hash mismatch")
	}

	if block.Header.Timestamp <= c.Tip().Header.Timestamp {
		return nil, fmt.Errorf("block timestamp must increase")
	}

	expectedBits := c.ExpectedBits(expectedHeight)
	if block.Header.Bits != expectedBits {
		return nil, fmt.Errorf("bits %08x do not match expected %08x", block.Header.Bits, expectedBits)
	}

	root, err := wire.CalcMerkleRoot(block.Transactions)
	if err != nil {
		return nil, err
	}
	if block.Header.MerkleRoot != root {
		return nil, fmt.Errorf("merkle root mismatch")
	}

	if err := consensus.CheckProofOfWork(&block.Header, c.params); err != nil {
		return nil, err
	}

	nextUTXOs := cloneUTXOSet(c.utxos)
	fees, nextUTXOs, err := c.validateRegularTransactions(block.Transactions[1:], nextUTXOs, expectedHeight)
	if err != nil {
		return nil, err
	}
	if err := c.validateAndConnectCoinbase(block, fees, nextUTXOs, expectedHeight); err != nil {
		return nil, err
	}
	return nextUTXOs, nil
}

func (c *Chain) validateAndConnectCoinbase(block *wire.MsgBlock, fees int64, view map[wire.OutPoint]*UTXOEntry, blockHeight uint32) error {
	if len(block.Transactions) == 0 {
		return fmt.Errorf("block has no transactions")
	}
	coinbase := block.Transactions[0]
	if !coinbase.IsCoinbase() {
		return fmt.Errorf("first transaction is not coinbase")
	}
	for i, tx := range block.Transactions[1:] {
		if tx.IsCoinbase() {
			return fmt.Errorf("extra coinbase transaction at index %d", i+1)
		}
	}
	if len(coinbase.TxOut) < 2 {
		return fmt.Errorf("coinbase must include miner and project outputs")
	}
	coinbaseTotal, err := validateTxOutputs(coinbase)
	if err != nil {
		return fmt.Errorf("coinbase: %w", err)
	}

	minerSubsidy, projectSubsidy := consensus.CalcBlockOneTimeSplit(int64(block.Header.Height), c.params)
	minerReward := minerSubsidy + fees
	if minerReward < minerSubsidy {
		return fmt.Errorf("miner reward overflow")
	}
	if coinbase.TxOut[0].Value != minerReward {
		return fmt.Errorf("miner reward %d does not match expected %d", coinbase.TxOut[0].Value, minerReward)
	}
	if coinbase.TxOut[1].Value != projectSubsidy {
		return fmt.Errorf("project subsidy %d does not match expected %d", coinbase.TxOut[1].Value, projectSubsidy)
	}
	expectedTotal := minerReward + projectSubsidy
	if expectedTotal < minerReward {
		return fmt.Errorf("coinbase reward overflow")
	}
	if coinbaseTotal != expectedTotal {
		return fmt.Errorf("coinbase output total %d does not match expected %d", coinbaseTotal, expectedTotal)
	}
	if !bytes.Equal(coinbase.TxOut[1].PkScript, c.params.ProjectPayoutScript) {
		return fmt.Errorf("project payout script mismatch")
	}
	connectTxOutputs(coinbase, view, blockHeight, true)
	return nil
}

func (c *Chain) validateRegularTransactions(txs []*wire.MsgTx, view map[wire.OutPoint]*UTXOEntry, spendHeight uint32) (int64, map[wire.OutPoint]*UTXOEntry, error) {
	var totalFees int64
	for txIndex, tx := range txs {
		if tx.IsCoinbase() {
			return 0, nil, fmt.Errorf("extra coinbase transaction at index %d", txIndex+1)
		}
		fee, err := c.validateAndConnectRegularTx(tx, view, spendHeight)
		if err != nil {
			return 0, nil, fmt.Errorf("transaction %d: %w", txIndex+1, err)
		}
		if totalFees > math.MaxInt64-fee {
			return 0, nil, fmt.Errorf("fee overflow")
		}
		totalFees += fee
	}
	return totalFees, view, nil
}

func (c *Chain) validateAndConnectRegularTx(tx *wire.MsgTx, view map[wire.OutPoint]*UTXOEntry, spendHeight uint32) (int64, error) {
	if len(tx.TxIn) == 0 {
		return 0, fmt.Errorf("regular transaction has no inputs")
	}

	outputTotal, err := validateTxOutputs(tx)
	if err != nil {
		return 0, err
	}

	spent := make(map[wire.OutPoint]struct{}, len(tx.TxIn))
	var inputTotal int64
	for inputIndex, txIn := range tx.TxIn {
		outpoint := txIn.PreviousOutPoint
		if _, ok := spent[outpoint]; ok {
			return 0, fmt.Errorf("duplicate input %s:%d", outpoint.Hash, outpoint.Index)
		}
		spent[outpoint] = struct{}{}

		prevOut, ok := view[outpoint]
		if !ok {
			return 0, fmt.Errorf("missing input %s:%d", outpoint.Hash, outpoint.Index)
		}
		if prevOut.Coinbase && spendHeight < prevOut.BlockHeight+c.params.CoinbaseMaturity {
			return 0, fmt.Errorf("coinbase input %s:%d is immature until height %d", outpoint.Hash, outpoint.Index, prevOut.BlockHeight+c.params.CoinbaseMaturity)
		}
		if err := txscript.VerifyP2PKHInput(tx, inputIndex, prevOut.TxOut.PkScript); err != nil {
			return 0, fmt.Errorf("input %d: %w", inputIndex, err)
		}
		if inputTotal > math.MaxInt64-prevOut.TxOut.Value {
			return 0, fmt.Errorf("input value overflow")
		}
		inputTotal += prevOut.TxOut.Value
		delete(view, outpoint)
	}

	if inputTotal < outputTotal {
		return 0, fmt.Errorf("input value %d is less than output value %d", inputTotal, outputTotal)
	}
	connectTxOutputs(tx, view, spendHeight, false)
	return inputTotal - outputTotal, nil
}

func validateTxOutputs(tx *wire.MsgTx) (int64, error) {
	if len(tx.TxOut) == 0 {
		return 0, fmt.Errorf("transaction has no outputs")
	}
	var total int64
	for i, txOut := range tx.TxOut {
		if txOut.Value < 0 {
			return 0, fmt.Errorf("output %d has negative value", i)
		}
		if total > math.MaxInt64-txOut.Value {
			return 0, fmt.Errorf("output value overflow")
		}
		total += txOut.Value
	}
	return total, nil
}

func connectTxOutputs(tx *wire.MsgTx, view map[wire.OutPoint]*UTXOEntry, blockHeight uint32, coinbase bool) {
	txHash := tx.MustTxHash()
	for i, txOut := range tx.TxOut {
		if txOut.Value == 0 {
			continue
		}
		view[wire.OutPoint{
			Hash:  txHash,
			Index: uint32(i),
		}] = &UTXOEntry{
			TxOut:       cloneTxOut(txOut),
			BlockHeight: blockHeight,
			Coinbase:    coinbase,
		}
	}
}

func cloneUTXOSet(utxos map[wire.OutPoint]*UTXOEntry) map[wire.OutPoint]*UTXOEntry {
	cloned := make(map[wire.OutPoint]*UTXOEntry, len(utxos))
	for outpoint, entry := range utxos {
		cloned[outpoint] = cloneUTXOEntry(entry)
	}
	return cloned
}

func cloneUTXOEntry(entry *UTXOEntry) *UTXOEntry {
	if entry == nil {
		return nil
	}
	return &UTXOEntry{
		TxOut:       cloneTxOut(&entry.TxOut),
		BlockHeight: entry.BlockHeight,
		Coinbase:    entry.Coinbase,
	}
}

func cloneTxOut(txOut *wire.TxOut) wire.TxOut {
	if txOut == nil {
		return wire.TxOut{}
	}
	return wire.TxOut{
		Value:    txOut.Value,
		PkScript: append([]byte(nil), txOut.PkScript...),
	}
}
