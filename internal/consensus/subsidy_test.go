package consensus

import (
	"testing"

	"github.com/pinancoin/pacd/internal/chaincfg"
)

func TestSubsidySchedule(t *testing.T) {
	params := chaincfg.MainNetParams()
	if got := CalcBlockSubsidy(0, params); got != 0 {
		t.Fatalf("genesis subsidy = %d, want 0", got)
	}
	if got := CalcBlockSubsidy(1, params); got != params.BaseSubsidy {
		t.Fatalf("height 1 subsidy = %d, want %d", got, params.BaseSubsidy)
	}
	wantReduced := params.BaseSubsidy * params.MulSubsidy / params.DivSubsidy
	if got := CalcBlockSubsidy(params.ReductionInterval, params); got != wantReduced {
		t.Fatalf("first reduction subsidy = %d, want %d", got, wantReduced)
	}
}

func TestCoinbaseSplit(t *testing.T) {
	params := chaincfg.MainNetParams()
	miner, project := CalcBlockOneTimeSplit(1, params)
	if miner+project != params.BaseSubsidy {
		t.Fatalf("split sum = %d, want %d", miner+project, params.BaseSubsidy)
	}
	if miner != 1_607_462_662 {
		t.Fatalf("miner subsidy = %d", miner)
	}
	if project != 84_603_299 {
		t.Fatalf("project subsidy = %d", project)
	}
}
