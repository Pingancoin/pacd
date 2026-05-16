package main

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
)

func TestFormatPAC(t *testing.T) {
	tests := map[int64]string{
		0:             "0",
		1:             "0.00000001",
		100_000_000:   "1",
		1_692_065_961: "16.92065961",
		-1:            "-0.00000001",
	}
	for atoms, want := range tests {
		if got := formatPAC(atoms); got != want {
			t.Fatalf("formatPAC(%d) = %q, want %q", atoms, got, want)
		}
	}
}

func TestMiningStartTime(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	genesisTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0).UTC()

	got, err := miningStartTime(chain, "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(genesisTime) {
		t.Fatalf("default start time = %s, want %s", got, genesisTime)
	}

	start := genesisTime.Add(params.TargetTimePerBlock).Format(time.RFC3339)
	got, err = miningStartTime(chain, start)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(genesisTime) {
		t.Fatalf("explicit start time anchor = %s, want %s", got, genesisTime)
	}

	if _, err := miningStartTime(chain, genesisTime.Format(time.RFC3339)); err == nil {
		t.Fatal("expected error for start time at genesis")
	}
}
