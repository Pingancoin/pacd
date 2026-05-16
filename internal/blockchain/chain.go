package blockchain

import (
	"bytes"
	"fmt"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/wire"
)

type Chain struct {
	params *chaincfg.Params
	blocks []*wire.MsgBlock
}

func New(params *chaincfg.Params) *Chain {
	return &Chain{
		params: params,
		blocks: []*wire.MsgBlock{params.GenesisBlock},
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
	if err := c.ValidateBlock(block); err != nil {
		return err
	}
	c.blocks = append(c.blocks, block)
	return nil
}

func (c *Chain) ValidateBlock(block *wire.MsgBlock) error {
	expectedHeight := c.Height() + 1
	if block.Header.Height != expectedHeight {
		return fmt.Errorf("height %d does not extend tip %d", block.Header.Height, c.Height())
	}

	tipHash := c.Tip().MustBlockHash()
	if block.Header.PrevBlock != tipHash {
		return fmt.Errorf("previous block hash mismatch")
	}

	if block.Header.Timestamp <= c.Tip().Header.Timestamp {
		return fmt.Errorf("block timestamp must increase")
	}

	expectedBits := c.ExpectedBits(expectedHeight)
	if block.Header.Bits != expectedBits {
		return fmt.Errorf("bits %08x do not match expected %08x", block.Header.Bits, expectedBits)
	}

	root, err := wire.CalcMerkleRoot(block.Transactions)
	if err != nil {
		return err
	}
	if block.Header.MerkleRoot != root {
		return fmt.Errorf("merkle root mismatch")
	}

	if err := consensus.CheckProofOfWork(&block.Header, c.params); err != nil {
		return err
	}
	return c.validateCoinbase(block)
}

func (c *Chain) validateCoinbase(block *wire.MsgBlock) error {
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

	minerSubsidy, projectSubsidy := consensus.CalcBlockOneTimeSplit(int64(block.Header.Height), c.params)
	if coinbase.TxOut[0].Value != minerSubsidy {
		return fmt.Errorf("miner subsidy %d does not match expected %d", coinbase.TxOut[0].Value, minerSubsidy)
	}
	if coinbase.TxOut[1].Value != projectSubsidy {
		return fmt.Errorf("project subsidy %d does not match expected %d", coinbase.TxOut[1].Value, projectSubsidy)
	}
	if !bytes.Equal(coinbase.TxOut[1].PkScript, c.params.ProjectPayoutScript) {
		return fmt.Errorf("project payout script mismatch")
	}
	return nil
}
