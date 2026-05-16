package p2p

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/Pingancoin/pacd/internal/wire"
)

const ProtocolVersion = uint32(1)

type Version struct {
	ProtocolVersion uint32
	Network         string
	GenesisHash     wire.Hash
	BestHeight      uint32
	Nonce           uint64
	UserAgent       string
	Timestamp       int64
}

func NewVersion(network string, genesisHash wire.Hash, bestHeight uint32, nonce uint64, userAgent string) Version {
	return Version{
		ProtocolVersion: ProtocolVersion,
		Network:         network,
		GenesisHash:     genesisHash,
		BestHeight:      bestHeight,
		Nonce:           nonce,
		UserAgent:       userAgent,
		Timestamp:       time.Now().UTC().Unix(),
	}
}

func (v Version) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, v.ProtocolVersion); err != nil {
		return nil, err
	}
	writeString(buf, v.Network)
	buf.Write(v.GenesisHash[:])
	if err := binary.Write(buf, binary.LittleEndian, v.BestHeight); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, v.Nonce); err != nil {
		return nil, err
	}
	writeString(buf, v.UserAgent)
	if err := binary.Write(buf, binary.LittleEndian, v.Timestamp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DeserializeVersion(payload []byte) (Version, error) {
	reader := bytes.NewReader(payload)
	var v Version
	if err := binary.Read(reader, binary.LittleEndian, &v.ProtocolVersion); err != nil {
		return v, err
	}
	network, err := readString(reader, 32)
	if err != nil {
		return v, err
	}
	v.Network = network
	if _, err := io.ReadFull(reader, v.GenesisHash[:]); err != nil {
		return v, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &v.BestHeight); err != nil {
		return v, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &v.Nonce); err != nil {
		return v, err
	}
	userAgent, err := readString(reader, 128)
	if err != nil {
		return v, err
	}
	v.UserAgent = userAgent
	if err := binary.Read(reader, binary.LittleEndian, &v.Timestamp); err != nil {
		return v, err
	}
	if reader.Len() != 0 {
		return v, fmt.Errorf("version payload has %d trailing byte(s)", reader.Len())
	}
	return v, nil
}

func writeString(buf *bytes.Buffer, s string) {
	if len(s) > 255 {
		s = s[:255]
	}
	buf.WriteByte(byte(len(s)))
	buf.WriteString(s)
}

func readString(reader *bytes.Reader, max int) (string, error) {
	lengthByte, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	length := int(lengthByte)
	if length > max {
		return "", fmt.Errorf("string length %d exceeds max %d", length, max)
	}
	if length > reader.Len() {
		return "", fmt.Errorf("string length %d exceeds remaining %d", length, reader.Len())
	}
	b := make([]byte, length)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", err
	}
	return string(b), nil
}
