package stratum

import (
	"encoding/hex"
	"sync"
	"testing"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wire"
)

func TestNotifyParamsUseDR5HeaderSegments(t *testing.T) {
	params := chaincfg.SimNetParams()
	chain := blockchain.New(params)
	server, err := New(chain, blockstore.New(t.TempDir()), &sync.Mutex{}, Options{
		ListenAddr:      "127.0.0.1:0",
		MinerScript:     []byte("miner"),
		ShareDifficulty: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	j, err := server.createJob()
	if err != nil {
		t.Fatal(err)
	}
	notify, err := notifyParams(j, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(notify) != 9 {
		t.Fatalf("notify param count = %d, want 9", len(notify))
	}
	if got := len(notify[1].(string)); got != 64 {
		t.Fatalf("previous hash hex length = %d, want 64", got)
	}
	if got := len(notify[2].(string)); got != 216 {
		t.Fatalf("gentx1 hex length = %d, want 216", got)
	}
	if got := len(notify[3].(string)); got != 8 {
		t.Fatalf("gentx2 hex length = %d, want 8", got)
	}
	header, err := j.block.Header.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if len(header) != wire.MaxBlockHeaderPayload {
		t.Fatalf("job header length = %d, want %d", len(header), wire.MaxBlockHeaderPayload)
	}
}

func TestSubmitExtraDataAcceptsFullOrMinerSuffix(t *testing.T) {
	sessionExtra := "01020304"
	full, err := submitExtraData(sessionExtra, "0102030405060708090a0b0c")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(full[:submitExtraDataSize]); got != "0102030405060708090a0b0c" {
		t.Fatalf("full extra data = %s", got)
	}

	suffix, err := submitExtraData(sessionExtra, "05060708090a0b0c")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(suffix[:submitExtraDataSize]); got != "0102030405060708090a0b0c" {
		t.Fatalf("suffix extra data = %s", got)
	}
}
