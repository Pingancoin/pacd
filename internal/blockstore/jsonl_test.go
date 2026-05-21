package blockstore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/mining"
)

func TestStoreLoadAppend(t *testing.T) {
	params := chaincfg.SimNetParams()
	store := blockstore.New(t.TempDir())

	chain, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 3; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
		if err := store.Append(block); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != chain.Height() {
		t.Fatalf("loaded height = %d, want %d", loaded.Height(), chain.Height())
	}
	if loaded.Tip().MustBlockHash() != chain.Tip().MustBlockHash() {
		t.Fatalf("loaded tip hash mismatch")
	}
	if loaded.TotalSubsidy() != chain.TotalSubsidy() {
		t.Fatalf("loaded subsidy = %d, want %d", loaded.TotalSubsidy(), chain.TotalSubsidy())
	}
}

func TestRepairTruncatesCorruptTail(t *testing.T) {
	params := chaincfg.SimNetParams()
	store := blockstore.New(t.TempDir())
	chain, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}

	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 3; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Append(block); err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	corruptFile(t, store.Path(), "\n{\"height\":999")
	if _, err := store.Load(params); err == nil {
		t.Fatal("expected corrupt store load error")
	}

	repaired, report, err := store.Repair(params)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired || report.BackupPath == "" || report.TruncatedAt <= 0 || !strings.Contains(report.Reason, "line 4") {
		t.Fatalf("unexpected repair report: %+v", report)
	}
	if repaired.Height() != 3 || repaired.Tip().MustBlockHash() != chain.Tip().MustBlockHash() {
		t.Fatalf("repaired chain mismatch: height=%d hash=%s", repaired.Height(), repaired.Tip().MustBlockHash())
	}
	if _, err := os.Stat(report.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "999") {
		t.Fatalf("corrupt tail was not truncated: %s", data)
	}
}

func TestRepairCanTruncateFullyCorruptStore(t *testing.T) {
	params := chaincfg.SimNetParams()
	store := blockstore.New(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repaired, report, err := store.Repair(params)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired || report.TruncatedAt != 0 {
		t.Fatalf("unexpected repair report: %+v", report)
	}
	if repaired.Height() != 0 {
		t.Fatalf("height = %d, want genesis", repaired.Height())
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("store size = %d, want 0", info.Size())
	}
}

func TestStoreReplace(t *testing.T) {
	params := chaincfg.SimNetParams()
	store := blockstore.New(t.TempDir())
	chain := blockchain.New(params)
	blockTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < 2; i++ {
		blockTime = blockTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, []byte("SsimMiner"), blockTime, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := chain.AddBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Replace(chain.Blocks()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != 2 || loaded.Tip().MustBlockHash() != chain.Tip().MustBlockHash() {
		t.Fatalf("loaded replacement height=%d hash=%s", loaded.Height(), loaded.Tip().MustBlockHash())
	}

	sideChain := blockchain.New(params)
	blockTime = time.Unix(params.GenesisBlock.Header.Timestamp, 0).Add(params.TargetTimePerBlock)
	sideBlock, err := mining.MineBlock(sideChain, []byte("SsimSide"), blockTime, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := sideChain.AddBlock(sideBlock); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(sideChain.Blocks()); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(params)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Height() != 1 || loaded.Tip().MustBlockHash() != sideBlock.MustBlockHash() {
		t.Fatalf("loaded rewritten height=%d hash=%s", loaded.Height(), loaded.Tip().MustBlockHash())
	}
}

func corruptFile(t *testing.T, path string, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
