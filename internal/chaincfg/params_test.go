package chaincfg_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
)

func TestMainnetMiningStartTime(t *testing.T) {
	params := chaincfg.MainNetParams()
	if params.MiningStartTime != chaincfg.MainNetMiningStartTime.Unix() {
		t.Fatalf("mainnet mining start = %d, want %d", params.MiningStartTime, chaincfg.MainNetMiningStartTime.Unix())
	}
	if chaincfg.MiningOpen(params, chaincfg.MainNetMiningStartTime.Add(-time.Second)) {
		t.Fatal("mainnet mining opened before launch time")
	}
	if !chaincfg.MiningOpen(params, chaincfg.MainNetMiningStartTime) {
		t.Fatal("mainnet mining did not open at launch time")
	}
}

func TestMainnetInitialDifficulty(t *testing.T) {
	params := chaincfg.MainNetParams()
	if params.GenesisBits != 0x1c2aaaaa {
		t.Fatalf("mainnet genesis bits = %08x, want 1c2aaaaa", params.GenesisBits)
	}
	if got := consensus.DifficultyRatio(params.GenesisBits, params).FloatString(4); got != "6.0000" {
		t.Fatalf("mainnet initial difficulty = %s, want 6.0000", got)
	}
}
