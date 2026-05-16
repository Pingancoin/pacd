package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/decred/dcrd/crypto/blake256"
)

const MaxUint32 = ^uint32(0)

type OutPoint struct {
	Hash  Hash
	Index uint32
}

type TxIn struct {
	PreviousOutPoint OutPoint
	SignatureScript  []byte
	Sequence         uint32
}

type TxOut struct {
	Value    int64
	PkScript []byte
}

type MsgTx struct {
	Version  int32
	TxIn     []*TxIn
	TxOut    []*TxOut
	LockTime uint32
}

func NewCoinbaseTx(height uint32, message string, outputs []*TxOut) *MsgTx {
	sigScript := new(bytes.Buffer)
	_ = binary.Write(sigScript, binary.LittleEndian, height)
	sigScript.WriteString(message)

	return &MsgTx{
		Version: 1,
		TxIn: []*TxIn{{
			PreviousOutPoint: OutPoint{
				Hash:  ZeroHash(),
				Index: MaxUint32,
			},
			SignatureScript: sigScript.Bytes(),
			Sequence:        MaxUint32,
		}},
		TxOut: outputs,
	}
}

func (tx *MsgTx) IsCoinbase() bool {
	return len(tx.TxIn) == 1 &&
		tx.TxIn[0].PreviousOutPoint.Hash == ZeroHash() &&
		tx.TxIn[0].PreviousOutPoint.Index == MaxUint32
}

func (tx *MsgTx) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, tx.Version); err != nil {
		return nil, err
	}
	writeVarInt(buf, uint64(len(tx.TxIn)))
	for _, in := range tx.TxIn {
		buf.Write(in.PreviousOutPoint.Hash[:])
		if err := binary.Write(buf, binary.LittleEndian, in.PreviousOutPoint.Index); err != nil {
			return nil, err
		}
		writeVarBytes(buf, in.SignatureScript)
		if err := binary.Write(buf, binary.LittleEndian, in.Sequence); err != nil {
			return nil, err
		}
	}
	writeVarInt(buf, uint64(len(tx.TxOut)))
	for _, out := range tx.TxOut {
		if err := binary.Write(buf, binary.LittleEndian, out.Value); err != nil {
			return nil, err
		}
		writeVarBytes(buf, out.PkScript)
	}
	if err := binary.Write(buf, binary.LittleEndian, tx.LockTime); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DeserializeTx(serialized []byte) (*MsgTx, error) {
	reader := bytes.NewReader(serialized)
	tx := &MsgTx{}
	if err := binary.Read(reader, binary.LittleEndian, &tx.Version); err != nil {
		return nil, err
	}

	inputCount, err := readVarInt(reader)
	if err != nil {
		return nil, err
	}
	tx.TxIn = make([]*TxIn, 0, inputCount)
	for i := uint64(0); i < inputCount; i++ {
		in := &TxIn{}
		if _, err := io.ReadFull(reader, in.PreviousOutPoint.Hash[:]); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &in.PreviousOutPoint.Index); err != nil {
			return nil, err
		}
		in.SignatureScript, err = readVarBytes(reader)
		if err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &in.Sequence); err != nil {
			return nil, err
		}
		tx.TxIn = append(tx.TxIn, in)
	}

	outputCount, err := readVarInt(reader)
	if err != nil {
		return nil, err
	}
	tx.TxOut = make([]*TxOut, 0, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		out := &TxOut{}
		if err := binary.Read(reader, binary.LittleEndian, &out.Value); err != nil {
			return nil, err
		}
		out.PkScript, err = readVarBytes(reader)
		if err != nil {
			return nil, err
		}
		tx.TxOut = append(tx.TxOut, out)
	}

	if err := binary.Read(reader, binary.LittleEndian, &tx.LockTime); err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("transaction has %d trailing byte(s)", reader.Len())
	}
	return tx, nil
}

func (tx *MsgTx) TxHash() (Hash, error) {
	serialized, err := tx.Serialize()
	if err != nil {
		return Hash{}, err
	}
	return blake256.Sum256(serialized), nil
}

func (tx *MsgTx) MustTxHash() Hash {
	hash, err := tx.TxHash()
	if err != nil {
		panic(fmt.Sprintf("tx hash failed: %v", err))
	}
	return hash
}

func writeVarBytes(buf *bytes.Buffer, b []byte) {
	writeVarInt(buf, uint64(len(b)))
	buf.Write(b)
}

func writeVarInt(buf *bytes.Buffer, val uint64) {
	switch {
	case val < 0xfd:
		buf.WriteByte(byte(val))
	case val <= 0xffff:
		buf.WriteByte(0xfd)
		_ = binary.Write(buf, binary.LittleEndian, uint16(val))
	case val <= 0xffffffff:
		buf.WriteByte(0xfe)
		_ = binary.Write(buf, binary.LittleEndian, uint32(val))
	default:
		buf.WriteByte(0xff)
		_ = binary.Write(buf, binary.LittleEndian, val)
	}
}

func readVarBytes(reader *bytes.Reader) ([]byte, error) {
	length, err := readVarInt(reader)
	if err != nil {
		return nil, err
	}
	if length > uint64(reader.Len()) {
		return nil, fmt.Errorf("var bytes length %d exceeds remaining %d", length, reader.Len())
	}
	b := make([]byte, length)
	if _, err := io.ReadFull(reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

func readVarInt(reader *bytes.Reader) (uint64, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return 0, err
	}
	switch prefix {
	case 0xfd:
		var v uint16
		if err := binary.Read(reader, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return uint64(v), nil
	case 0xfe:
		var v uint32
		if err := binary.Read(reader, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return uint64(v), nil
	case 0xff:
		var v uint64
		if err := binary.Read(reader, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return v, nil
	default:
		return uint64(prefix), nil
	}
}
