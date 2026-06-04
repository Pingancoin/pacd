package wire

import (
	"bytes"
	"testing"
)

func TestBlockHeaderSerializeDecredCompatibleLength(t *testing.T) {
	var extra [32]byte
	copy(extra[:], []byte("pac-stratum-extra"))
	header := BlockHeader{
		Version:      1,
		PrevBlock:    Hash{0x01, 0x02, 0x03},
		MerkleRoot:   Hash{0x04, 0x05, 0x06},
		Bits:         0x207fffff,
		SBits:        0,
		Height:       12,
		Size:         345,
		Timestamp:    1_780_000_000,
		Nonce:        99,
		ExtraData:    extra,
		StakeVersion: 0,
	}

	serialized, err := header.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if len(serialized) != MaxBlockHeaderPayload {
		t.Fatalf("serialized header length = %d, want %d", len(serialized), MaxBlockHeaderPayload)
	}

	roundTrip, err := DeserializeBlockHeader(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Version != header.Version ||
		roundTrip.PrevBlock != header.PrevBlock ||
		roundTrip.MerkleRoot != header.MerkleRoot ||
		roundTrip.Bits != header.Bits ||
		roundTrip.Height != header.Height ||
		roundTrip.Size != header.Size ||
		roundTrip.Timestamp != header.Timestamp ||
		roundTrip.Nonce != header.Nonce ||
		!bytes.Equal(roundTrip.ExtraData[:], header.ExtraData[:]) {
		t.Fatalf("header round trip mismatch: got %+v want %+v", roundTrip, header)
	}
}
