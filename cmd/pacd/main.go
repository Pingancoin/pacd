package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
)

func main() {
	network := flag.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	printParams := flag.Bool("printparams", false, "print consensus parameters")
	mineTo := flag.String("mine", "", "mine to a miner payout script/address label")
	blocks := flag.Int("blocks", 1, "number of blocks to mine")
	flag.Parse()

	params, err := selectParams(*network)
	if err != nil {
		exit(err)
	}

	if *printParams {
		printConsensusParams(params)
	}

	if *mineTo == "" {
		if !*printParams {
			printConsensusParams(params)
		}
		return
	}

	chain := blockchain.New(params)
	minerScript := []byte(*mineTo)
	nextTime := time.Unix(params.GenesisBlock.Header.Timestamp, 0)
	for i := 0; i < *blocks; i++ {
		nextTime = nextTime.Add(params.TargetTimePerBlock)
		block, err := mining.MineBlock(chain, minerScript, nextTime, 0)
		if err != nil {
			exit(err)
		}
		if err := chain.AddBlock(block); err != nil {
			exit(err)
		}
		hash := block.MustBlockHash()
		miner, project := consensus.CalcBlockOneTimeSplit(int64(block.Header.Height), params)
		fmt.Printf("mined height=%d hash=%s bits=%08x nonce=%d miner=%d project=%d\n",
			block.Header.Height, hash, block.Header.Bits, block.Header.Nonce, miner, project)
	}
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

func exit(err error) {
	fmt.Fprintln(os.Stderr, "pacd:", err)
	os.Exit(1)
}
