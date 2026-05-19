package main

import (
	"bytes"
	"io"
	"os"
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

func TestBuildLaunchCheckReportMainnetPlaceholder(t *testing.T) {
	report := buildLaunchCheckReport(chaincfg.MainNetParams())
	if report.Ready {
		t.Fatal("mainnet report should not be ready with placeholder payout script")
	}
	if report.ProjectPayoutScriptFrozen {
		t.Fatal("placeholder payout script reported as frozen")
	}
	if len(report.BlockingIssues) == 0 {
		t.Fatal("expected blocking issues")
	}
}

func TestBuildLaunchCheckReportFrozenConfig(t *testing.T) {
	params := chaincfg.MainNetParams()
	params.ProjectPayoutScript = []byte{0xa9, 0x14, 0x01, 0x87}

	report := buildLaunchCheckReport(params)
	if !report.Ready {
		t.Fatalf("report not ready: %+v", report)
	}
	if !report.ProjectPayoutScriptFrozen {
		t.Fatal("frozen payout script not detected")
	}
}

func TestBuildLaunchCheckReportSimnet(t *testing.T) {
	report := buildLaunchCheckReport(chaincfg.SimNetParams())
	if !report.Ready {
		t.Fatalf("simnet report not ready: %+v", report)
	}
}

func TestPrintLaunchCheckJSON(t *testing.T) {
	report := launchCheckReport{
		Network: "mainnet",
		Ready:   true,
	}
	var buf bytes.Buffer
	stdout := captureStdout(t, &buf, func() error {
		return printLaunchCheckJSON(report)
	})
	if stdout != nil {
		t.Fatal(stdout)
	}
	if got := buf.String(); got == "" || got[0] != '{' {
		t.Fatalf("json output = %q", got)
	}
}

func captureStdout(t *testing.T, target io.Writer, fn func() error) error {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	_, copyErr := io.Copy(target, r)
	_ = r.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	return runErr
}
