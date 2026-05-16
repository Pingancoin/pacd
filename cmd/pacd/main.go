package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wire"
)

func main() {
	network := flag.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	printParams := flag.Bool("printparams", false, "print consensus parameters")
	mineTo := flag.String("mine", "", "mine to a miner payout script/address label")
	blocks := flag.Int("blocks", 1, "number of blocks to mine")
	maxNonce := flag.Uint("maxnonce", 0, "maximum nonce to scan per block; 0 scans the full uint32 range")
	startTime := flag.String("starttime", "", "UTC start time for simnet mining in RFC3339 format")
	quiet := flag.Bool("quiet", false, "only print final mining summary")
	dataDir := flag.String("datadir", defaultDataDir(), "base directory for local chain data")
	reset := flag.Bool("reset", false, "delete existing simnet block data before starting")
	flag.Parse()

	params, err := selectParams(*network)
	if err != nil {
		exit(err)
	}

	if *printParams {
		printConsensusParams(params)
	}

	store := blockstore.New(filepath.Join(*dataDir, params.Name))
	if *network == "simnet" && *reset {
		if err := os.Remove(store.Path()); err != nil && !os.IsNotExist(err) {
			exit(err)
		}
	}
	chain, err := store.Load(params)
	if err != nil {
		exit(err)
	}

	if *mineTo == "" {
		if !*printParams {
			printConsensusParams(params)
		}
		printMiningSummary(chain, store)
		return
	}
	if *blocks <= 0 {
		exit(fmt.Errorf("blocks must be positive"))
	}
	if *network != "simnet" {
		exit(fmt.Errorf("local mining is currently intended for simnet only"))
	}
	if *maxNonce > uint(wire.MaxUint32) {
		exit(fmt.Errorf("maxnonce must be <= %d", wire.MaxUint32))
	}

	minerScript := []byte(*mineTo)
	nextTime, err := miningStartTime(chain, *startTime)
	if err != nil {
		exit(err)
	}

	printMiningHeader(params, store, *mineTo, *blocks, *quiet)
	for i := 0; i < *blocks; i++ {
		nextTime = nextTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, minerScript, nextTime, uint32(*maxNonce))
		if err != nil {
			exit(err)
		}
		if err := chain.AddBlock(block); err != nil {
			exit(err)
		}
		if err := store.Append(block); err != nil {
			exit(err)
		}
		if !*quiet {
			printMinedBlock(chain, block)
		}
	}
	printMiningSummary(chain, store)
}

func selectParams(network string) (*chaincfg.Params, error) {
	switch network {
	case "mainnet":
		return chaincfg.MainNetParams(), nil
	case "testnet":
		return chaincfg.TestNetParams(), nil
	case "simnet":
		return chaincfg.SimNetParams(), nil
	default:
		return nil, fmt.Errorf("unknown network %q", network)
	}
}

func printConsensusParams(params *chaincfg.Params) {
	total := consensus.EstimateTotalSubsidy(params)
	fmt.Printf("network: %s\n", params.Name)
	fmt.Printf("ticker: %s\n", params.Ticker)
	fmt.Printf("address prefix: %s...\n", params.AddressPrefix)
	fmt.Printf("genesis hash: %s\n", params.GenesisHash)
	fmt.Printf("genesis time: %s\n", time.Unix(params.GenesisBlock.Header.Timestamp, 0).UTC().Format(time.RFC3339))
	fmt.Printf("target block time: %s\n", params.TargetTimePerBlock)
	fmt.Printf("pow limit bits: %08x\n", params.PowLimitBits)
	fmt.Printf("genesis bits: %08x\n", params.GenesisBits)
	fmt.Printf("asert half life: %s\n", params.ASERTHalfLife)
	fmt.Printf("base subsidy: %.8f PAC\n", float64(params.BaseSubsidy)/float64(chaincfg.Coin))
	fmt.Printf("estimated total subsidy: %.8f PAC\n", float64(total)/float64(chaincfg.Coin))
	fmt.Printf("coinbase split: %d%% miner / %d%% project\n", params.MinerRewardPercent, params.ProjectRewardPercent)
	fmt.Printf("project multisig: %d-of-%d\n", params.ProjectMultisigM, params.ProjectMultisigN)
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pacd"
	}
	return filepath.Join(home, ".pacd")
}

func miningStartTime(chain *blockchain.Chain, startTime string) (time.Time, error) {
	params := chain.Params()
	if startTime == "" {
		return time.Unix(chain.Tip().Header.Timestamp, 0).UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, startTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid starttime %q: %w", startTime, err)
	}
	genesisTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	if !parsed.After(genesisTime) {
		return time.Time{}, fmt.Errorf("starttime must be after genesis time %s", genesisTime.UTC().Format(time.RFC3339))
	}
	return parsed.UTC().Add(-params.TargetTimePerBlock), nil
}

func printMiningHeader(params *chaincfg.Params, store *blockstore.Store, miner string, blocks int, quiet bool) {
	if quiet {
		return
	}
	fmt.Printf("mining %d %s block(s) to %q\n", blocks, params.Name, miner)
	fmt.Printf("data file: %s\n", store.Path())
	fmt.Printf("%-7s %-64s %-8s %-10s %-10s %-20s %-14s %-14s %-14s %-14s\n",
		"height", "hash", "bits", "diff", "nonce", "time", "subsidy", "miner", "project", "supply")
}

func printMinedBlock(chain *blockchain.Chain, block *wire.MsgBlock) {
	params := chain.Params()
	hash := block.MustBlockHash()
	miner, project := consensus.CalcBlockOneTimeSplit(int64(block.Header.Height), params)
	subsidy := miner + project
	difficulty := consensus.DifficultyRatio(block.Header.Bits, params).FloatString(4)
	fmt.Printf("%-7d %-64s %08x %-10s %-10d %-20s %-14s %-14s %-14s %-14s\n",
		block.Header.Height,
		hash,
		block.Header.Bits,
		difficulty,
		block.Header.Nonce,
		time.Unix(block.Header.Timestamp, 0).UTC().Format(time.RFC3339),
		formatPAC(subsidy),
		formatPAC(miner),
		formatPAC(project),
		formatPAC(chain.TotalSubsidy()),
	)
}

func printMiningSummary(chain *blockchain.Chain, store *blockstore.Store) {
	tip := chain.Tip()
	fmt.Printf("summary height=%d hash=%s supply=%s blocks=%d data=%s\n",
		chain.Height(),
		tip.MustBlockHash(),
		formatPAC(chain.TotalSubsidy()),
		len(chain.Blocks())-1,
		store.Path(),
	)
}

func formatPAC(atoms int64) string {
	sign := ""
	if atoms < 0 {
		sign = "-"
		atoms = -atoms
	}
	whole := atoms / chaincfg.Coin
	frac := atoms % chaincfg.Coin
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%s%d.%08d", sign, whole, frac), "0"), ".")
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "pacd:", err)
	os.Exit(1)
}
