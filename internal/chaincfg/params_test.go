package chaincfg_test

import (
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/chaincfg"
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

func TestStageNetParamsAreDistinctAndOpen(t *testing.T) {
	mainnet := chaincfg.MainNetParams()
	stagenet := chaincfg.StageNetParams()
	if stagenet.Name != "stagenet" {
		t.Fatalf("stagenet name = %q", stagenet.Name)
	}
	if stagenet.NetworkMagic == mainnet.NetworkMagic || stagenet.DefaultPort == mainnet.DefaultPort || stagenet.AddressPrefix == mainnet.AddressPrefix {
		t.Fatalf("stagenet is not distinct from mainnet: %+v", stagenet)
	}
	if !chaincfg.MiningOpen(stagenet, time.Now().UTC()) {
		t.Fatal("stagenet mining should be open")
	}
}
