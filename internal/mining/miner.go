package mining

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/wire"
)

func NewCandidate(chain *blockchain.Chain, minerScript []byte, timestamp time.Time) (*wire.MsgBlock, error) {
	nextHeight := chain.Height() + 1
	minerSubsidy, projectSubsidy := consensus.CalcBlockOneTimeSplit(int64(nextHeight), chain.Params())
	coinbase := wire.NewCoinbaseTx(nextHeight, "PAC", []*wire.TxOut{{
		Value:    minerSubsidy,
		PkScript: minerScript,
	}, {
		Value:    projectSubsidy,
		PkScript: chain.Params().ProjectPayoutScript,
	}})

	block := &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:   1,
			PrevBlock: chain.Tip().MustBlockHash(),
			Timestamp: timestamp.Unix(),
			Bits:      chain.ExpectedBits(nextHeight),
			Height:    nextHeight,
		},
		Transactions: []*wire.MsgTx{coinbase},
	}
	if err := block.RefreshMerkleRoot(); err != nil {
		return nil, err
	}
	return block, nil
}

func MineBlock(chain *blockchain.Chain, minerScript []byte, timestamp time.Time, maxNonce uint32) (*wire.MsgBlock, error) {
	if timestamp.Unix() <= chain.Tip().Header.Timestamp {
		return nil, fmt.Errorf("timestamp %s must be after chain tip", timestamp.UTC().Format(time.RFC3339))
	}

	block, err := NewCandidate(chain, minerScript, timestamp)
	if err != nil {
		return nil, err
	}
	if maxNonce == 0 {
		maxNonce = math.MaxUint32
	}

	for nonce := uint32(0); nonce <= maxNonce; nonce++ {
		block.Header.Nonce = nonce
		if consensus.CheckProofOfWork(&block.Header, chain.Params()) == nil {
			return block, nil
		}
		if nonce == math.MaxUint32 {
			break
		}
	}
	return nil, errors.New("nonce space exhausted")
}
