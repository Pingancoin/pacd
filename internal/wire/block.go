package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/decred/dcrd/crypto/blake256"
)

const MaxBlockHeaderPayload = 180

type BlockHeader struct {
	Version      int32
	PrevBlock    Hash
	MerkleRoot   Hash
	StakeRoot    Hash
	VoteBits     uint16
	FinalState   [6]byte
	Voters       uint16
	FreshStake   uint8
	Revocations  uint8
	PoolSize     uint32
	Bits         uint32
	SBits        int64
	Height       uint32
	Size         uint32
	Timestamp    int64
	Nonce        uint32
	ExtraData    [32]byte
	StakeVersion uint32
}

type MsgBlock struct {
	Header       BlockHeader
	Transactions []*MsgTx
}

func (h *BlockHeader) Serialize() ([]byte, error) {
	if h.Timestamp < 0 || h.Timestamp > math.MaxUint32 {
		return nil, fmt.Errorf("timestamp %d is outside uint32 header range", h.Timestamp)
	}
	buf := bytes.NewBuffer(make([]byte, 0, MaxBlockHeaderPayload))
	if err := binary.Write(buf, binary.LittleEndian, h.Version); err != nil {
		return nil, err
	}
	buf.Write(h.PrevBlock[:])
	buf.Write(h.MerkleRoot[:])
	buf.Write(h.StakeRoot[:])
	if err := binary.Write(buf, binary.LittleEndian, h.VoteBits); err != nil {
		return nil, err
	}
	buf.Write(h.FinalState[:])
	if err := binary.Write(buf, binary.LittleEndian, h.Voters); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.FreshStake); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Revocations); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.PoolSize); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Bits); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.SBits); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Height); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Size); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(h.Timestamp)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, h.Nonce); err != nil {
		return nil, err
	}
	buf.Write(h.ExtraData[:])
	if err := binary.Write(buf, binary.LittleEndian, h.StakeVersion); err != nil {
		return nil, err
	}
	if buf.Len() != MaxBlockHeaderPayload {
		return nil, fmt.Errorf("serialized header length is %d, want %d", buf.Len(), MaxBlockHeaderPayload)
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

func (b *MsgBlock) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)
	header, err := b.Header.Serialize()
	if err != nil {
		return nil, err
	}
	buf.Write(header)
	writeVarInt(buf, uint64(len(b.Transactions)))
	for _, tx := range b.Transactions {
		serializedTx, err := tx.Serialize()
		if err != nil {
			return nil, err
		}
		writeVarBytes(buf, serializedTx)
	}
	return buf.Bytes(), nil
}

func (b *MsgBlock) SerializedSize() (uint32, error) {
	serialized, err := b.Serialize()
	if err != nil {
		return 0, err
	}
	if len(serialized) > math.MaxUint32 {
		return 0, fmt.Errorf("serialized block length %d exceeds uint32", len(serialized))
	}
	return uint32(len(serialized)), nil
}

func (b *MsgBlock) RefreshHeaderSize() error {
	size, err := b.SerializedSize()
	if err != nil {
		return err
	}
	b.Header.Size = size
	return nil
}

func DeserializeBlock(serialized []byte) (*MsgBlock, error) {
	reader := bytes.NewReader(serialized)
	header, err := deserializeBlockHeader(reader)
	if err != nil {
		return nil, err
	}
	txCount, err := readVarInt(reader)
	if err != nil {
		return nil, err
	}
	block := &MsgBlock{
		Header:       header,
		Transactions: make([]*MsgTx, 0, txCount),
	}
	for i := uint64(0); i < txCount; i++ {
		serializedTx, err := readVarBytes(reader)
		if err != nil {
			return nil, err
		}
		tx, err := DeserializeTx(serializedTx)
		if err != nil {
			return nil, err
		}
		block.Transactions = append(block.Transactions, tx)
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("block has %d trailing byte(s)", reader.Len())
	}
	return block, nil
}

func DeserializeBlockHeader(serialized []byte) (BlockHeader, error) {
	reader := bytes.NewReader(serialized)
	header, err := deserializeBlockHeader(reader)
	if err != nil {
		return BlockHeader{}, err
	}
	if reader.Len() != 0 {
		return BlockHeader{}, fmt.Errorf("block header has %d trailing byte(s)", reader.Len())
	}
	return header, nil
}

func deserializeBlockHeader(reader *bytes.Reader) (BlockHeader, error) {
	var header BlockHeader
	if err := binary.Read(reader, binary.LittleEndian, &header.Version); err != nil {
		return header, err
	}
	if _, err := io.ReadFull(reader, header.PrevBlock[:]); err != nil {
		return header, err
	}
	if _, err := io.ReadFull(reader, header.MerkleRoot[:]); err != nil {
		return header, err
	}
	if _, err := io.ReadFull(reader, header.StakeRoot[:]); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.VoteBits); err != nil {
		return header, err
	}
	if _, err := io.ReadFull(reader, header.FinalState[:]); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Voters); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.FreshStake); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Revocations); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.PoolSize); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Bits); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.SBits); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Height); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Size); err != nil {
		return header, err
	}
	var timestamp uint32
	if err := binary.Read(reader, binary.LittleEndian, &timestamp); err != nil {
		return header, err
	}
	header.Timestamp = int64(timestamp)
	if err := binary.Read(reader, binary.LittleEndian, &header.Nonce); err != nil {
		return header, err
	}
	if _, err := io.ReadFull(reader, header.ExtraData[:]); err != nil {
		return header, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.StakeVersion); err != nil {
		return header, err
	}
	return header, nil
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
