package p2p

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/Pingancoin/pacd/internal/wire"
)

const (
	MaxHeadersPerMessage = 2000
	MaxBlocksPerRequest  = 500
)

type GetHeaders struct {
	StartHeight uint32
}

type Headers struct {
	Headers []wire.BlockHeader
}

type GetBlocks struct {
	Hashes []wire.Hash
}

func (g GetHeaders) Serialize() ([]byte, error) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], g.StartHeight)
	return b[:], nil
}

func DeserializeGetHeaders(payload []byte) (GetHeaders, error) {
	if len(payload) != 4 {
		return GetHeaders{}, fmt.Errorf("getheaders payload length is %d, want 4", len(payload))
	}
	return GetHeaders{StartHeight: binary.LittleEndian.Uint32(payload)}, nil
}

func (h Headers) Serialize() ([]byte, error) {
	if len(h.Headers) > MaxHeadersPerMessage {
		return nil, fmt.Errorf("headers count %d exceeds max %d", len(h.Headers), MaxHeadersPerMessage)
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(h.Headers))); err != nil {
		return nil, err
	}
	for _, header := range h.Headers {
		serialized, err := header.Serialize()
		if err != nil {
			return nil, err
		}
		buf.Write(serialized)
	}
	return buf.Bytes(), nil
}

func DeserializeHeaders(payload []byte) (Headers, error) {
	reader := bytes.NewReader(payload)
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return Headers{}, err
	}
	if count > MaxHeadersPerMessage {
		return Headers{}, fmt.Errorf("headers count %d exceeds max %d", count, MaxHeadersPerMessage)
	}
	headers := make([]wire.BlockHeader, 0, count)
	for i := uint32(0); i < count; i++ {
		headerBytes := make([]byte, 88)
		if _, err := io.ReadFull(reader, headerBytes); err != nil {
			return Headers{}, err
		}
		header, err := wire.DeserializeBlockHeader(headerBytes)
		if err != nil {
			return Headers{}, err
		}
		headers = append(headers, header)
	}
	if reader.Len() != 0 {
		return Headers{}, fmt.Errorf("headers payload has %d trailing byte(s)", reader.Len())
	}
	return Headers{Headers: headers}, nil
}

func (g GetBlocks) Serialize() ([]byte, error) {
	if len(g.Hashes) > MaxBlocksPerRequest {
		return nil, fmt.Errorf("block request count %d exceeds max %d", len(g.Hashes), MaxBlocksPerRequest)
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(g.Hashes))); err != nil {
		return nil, err
	}
	for _, hash := range g.Hashes {
		buf.Write(hash[:])
	}
	return buf.Bytes(), nil
}

func DeserializeGetBlocks(payload []byte) (GetBlocks, error) {
	reader := bytes.NewReader(payload)
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return GetBlocks{}, err
	}
	if count > MaxBlocksPerRequest {
		return GetBlocks{}, fmt.Errorf("block request count %d exceeds max %d", count, MaxBlocksPerRequest)
	}
	hashes := make([]wire.Hash, 0, count)
	for i := uint32(0); i < count; i++ {
		var hash wire.Hash
		if _, err := io.ReadFull(reader, hash[:]); err != nil {
			return GetBlocks{}, err
		}
		hashes = append(hashes, hash)
	}
	if reader.Len() != 0 {
		return GetBlocks{}, fmt.Errorf("getblocks payload has %d trailing byte(s)", reader.Len())
	}
	return GetBlocks{Hashes: hashes}, nil
}
