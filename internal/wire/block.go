package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/decred/dcrd/crypto/blake256"
)

type BlockHeader struct {
	Version    int32
	PrevBlock  Hash
	MerkleRoot Hash
	Timestamp  int64
	Bits       uint32
	Nonce      uint32
	Height     uint32
}

type MsgBlock struct {
	Header       BlockHeader
	Transactions []*MsgTx
}

func (h *BlockHeader) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, h.Version); err != nil {
		return nil, err
	}
	buf.Write(h.PrevBlock[:])
	buf.Write(h.MerkleRoot[:])
	if err := binary.Write(buf, binary.LittleEndian, h.Timestamp); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Bits); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Nonce); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Height); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (h *BlockHeader) BlockHash() (Hash, error) {
	serialized, err := h.Serialize()
	if err != nil {
		return Hash{}, err
	}
	return blake256.Sum256(serialized), nil
}

func (h *BlockHeader) MustBlockHash() Hash {
	hash, err := h.BlockHash()
	if err != nil {
		panic(fmt.Sprintf("block hash failed: %v", err))
	}
	return hash
}

func (b *MsgBlock) BlockHash() (Hash, error) {
	return b.Header.BlockHash()
}

func (b *MsgBlock) MustBlockHash() Hash {
	return b.Header.MustBlockHash()
}

func CalcMerkleRoot(txs []*MsgTx) (Hash, error) {
	if len(txs) == 0 {
		return ZeroHash(), nil
	}

	layer := make([]Hash, 0, len(txs))
	for _, tx := range txs {
		hash, err := tx.TxHash()
		if err != nil {
			return Hash{}, err
		}
		layer = append(layer, hash)
	}

	for len(layer) > 1 {
		next := make([]Hash, 0, (len(layer)+1)/2)
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left
			if i+1 < len(layer) {
				right = layer[i+1]
			}
			pair := make([]byte, 0, 64)
			pair = append(pair, left[:]...)
			pair = append(pair, right[:]...)
			next = append(next, blake256.Sum256(pair))
		}
		layer = next
	}
	return layer[0], nil
}

func (b *MsgBlock) RefreshMerkleRoot() error {
	root, err := CalcMerkleRoot(b.Transactions)
	if err != nil {
		return err
	}
	b.Header.MerkleRoot = root
	return nil
}
