package wire

import (
	"encoding/hex"
	"fmt"
)

// Hash is a 32-byte BLAKE-256 digest used by PAC block and transaction IDs.
type Hash [32]byte

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

func NewHashFromBytes(b []byte) (Hash, error) {
	var h Hash
	if len(b) != len(h) {
		return h, fmt.Errorf("hash length is %d, want %d", len(b), len(h))
	}
	copy(h[:], b)
	return h, nil
}

func ZeroHash() Hash {
	return Hash{}
}
