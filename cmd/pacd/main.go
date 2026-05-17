package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/p2p"
	"github.com/Pingancoin/pacd/internal/rpcserver"
	"github.com/Pingancoin/pacd/internal/wire"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "address" {
		if err := runAddressCommand(os.Args[2:]); err != nil {
			exit(err)
		}
		return
	}

	network := flag.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	printParams := flag.Bool("printparams", false, "print consensus parameters")
	mineTo := flag.String("mine", "", "mine to a miner payout script/address label")
	blocks := flag.Int("blocks", 1, "number of blocks to mine")
	maxNonce := flag.Uint("maxnonce", 0, "maximum nonce to scan per block; 0 scans the full uint32 range")
	startTime := flag.String("starttime", "", "UTC start time for simnet mining in RFC3339 format")
	quiet := flag.Bool("quiet", false, "only print final mining summary")
	dataDir := flag.String("datadir", defaultDataDir(), "base directory for local chain data")
	reset := flag.Bool("reset", false, "delete existing simnet block data before starting")
	rpc := flag.Bool("rpc", false, "start the local read-only HTTP RPC server")
	rpcListen := flag.String("rpclisten", "127.0.0.1:9509", "HTTP RPC listen address")
	p2pEnabled := flag.Bool("p2p", false, "start the P2P listener and peer manager")
	p2pListen := flag.String("listen", "", "P2P listen address; defaults to 127.0.0.1:<network default port> when --p2p is set")
	maxPeers := flag.Int("maxpeers", 32, "maximum P2P peers")
	var connectPeers stringList
	flag.Var(&connectPeers, "connect", "P2P peer address to connect to; repeat for multiple peers")
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
		if *rpc || *p2pEnabled {
			runServices(chain, store, *rpc, *rpcListen, *p2pEnabled, *p2pListen, connectPeers, *maxPeers)
		}
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

	minerScript, err := minerPayoutScript(params, *mineTo)
	if err != nil {
		exit(err)
	}
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
	if *rpc || *p2pEnabled {
		runServices(chain, store, *rpc, *rpcListen, *p2pEnabled, *p2pListen, connectPeers, *maxPeers)
	}
}

func runAddressCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("address subcommand required: pubkey or multisig")
	}
	switch args[0] {
	case "pubkey":
		return runPubKeyAddressCommand(args[1:])
	case "multisig":
		return runMultiSigAddressCommand(args[1:])
	case "validate-project":
		return runValidateProjectAddressCommand(args[1:])
	default:
		return fmt.Errorf("unknown address subcommand %q", args[0])
	}
}

func runPubKeyAddressCommand(args []string) error {
	flags := flag.NewFlagSet("pacd address pubkey", flag.ContinueOnError)
	network := flags.String("network", "mainnet", "network to use: mainnet, testnet, simnet")
	pubKeyHex := flags.String("pubkey", "", "compressed or uncompressed public key hex")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	pubKey, err := hex.DecodeString(*pubKeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex: %w", err)
	}
	addr, pubKeyHash, pkScript, err := address.AddressFromPubKey(params, pubKey)
	if err != nil {
		return err
	}
	fmt.Printf("network: %s\n", params.Name)
	fmt.Printf("address: %s\n", addr)
	fmt.Printf("pubkey_hash: %s\n", hex.EncodeToString(pubKeyHash))
	fmt.Printf("p2pkh_script: %s\n", hex.EncodeToString(pkScript))
	return nil
}

func runMultiSigAddressCommand(args []string) error {
	var pubKeys pubKeyList
	flags := flag.NewFlagSet("pacd address multisig", flag.ContinueOnError)
	network := flags.String("network", "mainnet", "network to use: mainnet, testnet, simnet")
	required := flags.Int("required", 3, "required signatures")
	flags.Var(&pubKeys, "pubkey", "compressed or uncompressed public key hex; repeat for each key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	script, err := address.MultiSigRedeemScript(*required, pubKeys)
	if err != nil {
		return err
	}
	addr, scriptHash, pkScript, err := address.AddressFromScript(params, script)
	if err != nil {
		return err
	}
	fmt.Printf("network: %s\n", params.Name)
	fmt.Printf("address: %s\n", addr)
	fmt.Printf("required: %d\n", *required)
	fmt.Printf("pubkeys: %d\n", len(pubKeys))
	fmt.Printf("script_hash: %s\n", hex.EncodeToString(scriptHash))
	fmt.Printf("redeem_script: %s\n", hex.EncodeToString(script))
	fmt.Printf("p2sh_script: %s\n", hex.EncodeToString(pkScript))
	if params.Name == "mainnet" {
		fmt.Printf("chaincfg_project_payout_script: %s\n", goByteSliceLiteral(pkScript))
	}
	return nil
}

func runValidateProjectAddressCommand(args []string) error {
	flags := flag.NewFlagSet("pacd address validate-project", flag.ContinueOnError)
	redeemScriptHex := flags.String("redeemscript", "", "project multisig redeem script hex")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params := chaincfg.MainNetParams()
	redeemScript, err := hex.DecodeString(*redeemScriptHex)
	if err != nil {
		return fmt.Errorf("invalid redeemscript hex: %w", err)
	}
	addr, scriptHash, pkScript, err := address.AddressFromScript(params, redeemScript)
	if err != nil {
		return err
	}
	fmt.Printf("network: %s\n", params.Name)
	fmt.Printf("address: %s\n", addr)
	fmt.Printf("script_hash: %s\n", hex.EncodeToString(scriptHash))
	fmt.Printf("p2sh_script: %s\n", hex.EncodeToString(pkScript))
	if chaincfg.MainNetProjectPayoutIsPlaceholder(params) {
		fmt.Println("status: mainnet project payout script is still placeholder")
		fmt.Printf("replace_with: %s\n", goByteSliceLiteral(pkScript))
		return nil
	}
	if !bytes.Equal(params.ProjectPayoutScript, pkScript) {
		return fmt.Errorf("mainnet project payout script does not match redeem script")
	}
	fmt.Println("status: mainnet project payout script matches redeem script")
	return nil
}

type pubKeyList [][]byte

func (p *pubKeyList) String() string {
	return fmt.Sprintf("%d pubkey(s)", len(*p))
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty peer address")
	}
	*s = append(*s, value)
	return nil
}

func (p *pubKeyList) Set(value string) error {
	pubKey, err := hex.DecodeString(value)
	if err != nil {
		return err
	}
	*p = append(*p, pubKey)
	return nil
}

func goByteSliceLiteral(b []byte) string {
	parts := make([]string, 0, len(b))
	for _, v := range b {
		parts = append(parts, fmt.Sprintf("0x%02x", v))
	}
	return "[]byte{" + strings.Join(parts, ", ") + "}"
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
	fmt.Printf("coinbase maturity: %d block(s)\n", params.CoinbaseMaturity)
	fmt.Printf("coinbase split: %d%% miner / %d%% project\n", params.MinerRewardPercent, params.ProjectRewardPercent)
	fmt.Printf("project multisig: %d-of-%d\n", params.ProjectMultisigM, params.ProjectMultisigN)
	if len(params.DNSSeeds) > 0 {
		fmt.Printf("dns seeds: %s\n", strings.Join(params.DNSSeeds, ","))
	}
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

func minerPayoutScript(params *chaincfg.Params, mineTo string) ([]byte, error) {
	if script, err := address.DecodeAddressScript(params, mineTo); err == nil {
		return script, nil
	}
	return []byte(mineTo), nil
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

func runServices(chain *blockchain.Chain, store *blockstore.Store, rpcEnabled bool, rpcListen string, p2pEnabled bool, p2pListen string, connectPeers []string, maxPeers int) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chainMu := &sync.Mutex{}
	errCh := make(chan error, 2)
	var node *p2p.Node
	var server *rpcserver.Server
	if p2pEnabled {
		listen := p2pListen
		if listen == "" {
			listen = "127.0.0.1:" + chain.Params().DefaultPort
		}
		peers := append([]string(nil), connectPeers...)
		if len(peers) == 0 {
			peers = seedPeers(chain.Params())
		}
		var err error
		node, err = p2p.NewNode(p2p.Config{
			Params:       chain.Params(),
			ListenAddr:   listen,
			Connect:      peers,
			MaxPeers:     maxPeers,
			Chain:        chain,
			Store:        store,
			ChainMu:      chainMu,
			AddrBookPath: filepath.Join(filepath.Dir(store.Path()), "peers.json"),
			Logger:       log.New(os.Stdout, "", 0),
		})
		if err != nil {
			exit(err)
		}
		fmt.Printf("p2p listening on %s\n", listen)
		if len(peers) > 0 {
			fmt.Printf("p2p connecting to %s\n", strings.Join(peers, ","))
		}
	}
	if rpcEnabled {
		server = rpcserver.NewWithLock(chain, store, chainMu)
		if node != nil {
			server.SetBlockConnectedCallback(node.RelayBlock)
			server.SetTransactionAcceptedCallback(node.RelayTransaction)
			server.SetPeerCallbacks(peerSnapshots(node), node.PeerCount, node.KnownAddressCount)
			node.SetBlockConnectedCallback(server.NotifyBlockConnected)
			node.SetTransactionCallbacks(server.HasTransaction, server.TransactionByHash, server.AcceptTransaction)
		}
	}
	if rpcEnabled {
		fmt.Printf("rpc listening on http://%s\n", rpcListen)
		go func() {
			errCh <- server.ListenAndServe(ctx, rpcListen)
		}()
	}
	if node != nil {
		go func() {
			errCh <- node.Start(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		return
	case err := <-errCh:
		if err != nil {
			exit(err)
		}
	}
}

func seedPeers(params *chaincfg.Params) []string {
	peers := make([]string, 0, len(params.DNSSeeds))
	for _, seed := range params.DNSSeeds {
		if strings.Contains(seed, ":") {
			peers = append(peers, seed)
			continue
		}
		peers = append(peers, seed+":"+params.DefaultPort)
	}
	return peers
}

func peerSnapshots(node *p2p.Node) func() []rpcserver.PeerSnapshot {
	return func() []rpcserver.PeerSnapshot {
		peers := node.Peers()
		out := make([]rpcserver.PeerSnapshot, 0, len(peers))
		for _, peer := range peers {
			out = append(out, rpcserver.PeerSnapshot{
				Address:     peer.Address,
				Inbound:     peer.Inbound,
				BestHeight:  peer.BestHeight,
				UserAgent:   peer.UserAgent,
				ConnectedAt: peer.ConnectedAt,
			})
		}
		return out
	}
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
