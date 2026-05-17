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
	MaxInventoryItems    = 1000
	MaxAddrItems         = 1000
	MaxAddrLength        = 255

	InvTypeBlock uint32 = 2
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

type InvVector struct {
	Type uint32
	Hash wire.Hash
}

type Inventory struct {
	Items []InvVector
}

type AddrList struct {
	Addrs []string
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

func (inv Inventory) Serialize() ([]byte, error) {
	if len(inv.Items) > MaxInventoryItems {
		return nil, fmt.Errorf("inventory count %d exceeds max %d", len(inv.Items), MaxInventoryItems)
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(inv.Items))); err != nil {
		return nil, err
	}
	for _, item := range inv.Items {
		if err := binary.Write(buf, binary.LittleEndian, item.Type); err != nil {
			return nil, err
		}
		buf.Write(item.Hash[:])
	}
	return buf.Bytes(), nil
}

func DeserializeInventory(payload []byte) (Inventory, error) {
	reader := bytes.NewReader(payload)
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return Inventory{}, err
	}
	if count > MaxInventoryItems {
		return Inventory{}, fmt.Errorf("inventory count %d exceeds max %d", count, MaxInventoryItems)
	}
	items := make([]InvVector, 0, count)
	for i := uint32(0); i < count; i++ {
		var item InvVector
		if err := binary.Read(reader, binary.LittleEndian, &item.Type); err != nil {
			return Inventory{}, err
		}
		if _, err := io.ReadFull(reader, item.Hash[:]); err != nil {
			return Inventory{}, err
		}
		items = append(items, item)
	}
	if reader.Len() != 0 {
		return Inventory{}, fmt.Errorf("inventory payload has %d trailing byte(s)", reader.Len())
	}
	return Inventory{Items: items}, nil
}

func (a AddrList) Serialize() ([]byte, error) {
	if len(a.Addrs) > MaxAddrItems {
		return nil, fmt.Errorf("addr count %d exceeds max %d", len(a.Addrs), MaxAddrItems)
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(a.Addrs))); err != nil {
		return nil, err
	}
	for _, addr := range a.Addrs {
		if len(addr) > MaxAddrLength {
			return nil, fmt.Errorf("addr length %d exceeds max %d", len(addr), MaxAddrLength)
		}
		buf.WriteByte(byte(len(addr)))
		buf.WriteString(addr)
	}
	return buf.Bytes(), nil
}

func DeserializeAddrList(payload []byte) (AddrList, error) {
	reader := bytes.NewReader(payload)
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return AddrList{}, err
	}
	if count > MaxAddrItems {
		return AddrList{}, fmt.Errorf("addr count %d exceeds max %d", count, MaxAddrItems)
	}
	addrs := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		addrLen, err := reader.ReadByte()
		if err != nil {
			return AddrList{}, err
		}
		addr := make([]byte, int(addrLen))
		if _, err := io.ReadFull(reader, addr); err != nil {
			return AddrList{}, err
		}
		addrs = append(addrs, string(addr))
	}
	if reader.Len() != 0 {
		return AddrList{}, fmt.Errorf("addr payload has %d trailing byte(s)", reader.Len())
	}
	return AddrList{Addrs: addrs}, nil
}
