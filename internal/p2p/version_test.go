package p2p_test

import (
	"testing"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/p2p"
)

func TestVersionRoundTrip(t *testing.T) {
	params := chaincfg.SimNetParams()
	version := p2p.NewVersion(params.Name, params.GenesisHash, 42, 99, "/test/")
	serialized, err := version.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	got, err := p2p.DeserializeVersion(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if got.Network != version.Network ||
		got.GenesisHash != version.GenesisHash ||
		got.BestHeight != version.BestHeight ||
		got.Nonce != version.Nonce ||
		got.UserAgent != version.UserAgent {
		t.Fatalf("version mismatch: got %+v want %+v", got, version)
	}
}
