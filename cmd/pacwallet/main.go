package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wallet"
)

func main() {
	if len(os.Args) < 2 {
		exit(fmt.Errorf("command required: create, newaddress, list, pubkeys, balance, drafttx, send"))
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		exit(err)
	}
}

func run(command string, args []string) error {
	switch command {
	case "create":
		return create(args)
	case "newaddress":
		return newAddress(args)
	case "list":
		return list(args)
	case "pubkeys":
		return pubKeys(args)
	case "balance":
		return balance(args)
	case "drafttx":
		return draftTx(args)
	case "send":
		return send(args)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func create(args []string) error {
	flags := newFlagSet("create")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	path := wallet.Path(*walletDir, params.Name)
	w, err := wallet.Create(path, params)
	if err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	printKey(w.Keys[0], false)
	fmt.Println("warning: wallet file is not encrypted yet; protect this file")
	return nil
}

func newAddress(args []string) error {
	flags := newFlagSet("newaddress")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	label := flags.String("label", "", "address label")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	path := wallet.Path(*walletDir, params.Name)
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	if err := w.AddKey(params, *label); err != nil {
		return err
	}
	if err := wallet.Save(path, w); err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	printKey(w.Keys[len(w.Keys)-1], false)
	return nil
}

func list(args []string) error {
	flags := newFlagSet("list")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	showPrivate := flags.Bool("show-private", false, "show private keys")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	w, err := wallet.Load(wallet.Path(*walletDir, params.Name))
	if err != nil {
		return err
	}
	for _, key := range w.Keys {
		printKey(key, *showPrivate)
	}
	return nil
}

func pubKeys(args []string) error {
	flags := newFlagSet("pubkeys")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	w, err := wallet.Load(wallet.Path(*walletDir, params.Name))
	if err != nil {
		return err
	}
	for _, key := range w.Keys {
		fmt.Printf("%s %s %s\n", key.Label, key.Address, key.PubKeyHex)
	}
	return nil
}

func balance(args []string) error {
	flags := newFlagSet("balance")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	rpcURL := flags.String("rpc", "http://127.0.0.1:9509", "pacd RPC URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	w, err := wallet.Load(wallet.Path(*walletDir, params.Name))
	if err != nil {
		return err
	}
	balance, err := wallet.ScanBalance(params, w, *rpcURL)
	if err != nil {
		return err
	}
	fmt.Printf("best_height: %d\n", balance.BestHeight)
	fmt.Printf("best_hash: %s\n", balance.BestHash)
	fmt.Printf("total: %s PAC\n", formatPAC(balance.Total))
	fmt.Printf("spendable: %s PAC\n", formatPAC(balance.Spendable))
	fmt.Printf("utxos: %d\n", balance.UTXOCount)
	for _, utxo := range balance.UTXOs {
		fmt.Printf("%s:%d %s PAC %s height=%d\n",
			utxo.TxHash, utxo.Vout, formatPAC(utxo.Value), utxo.Address, utxo.Height)
	}
	return nil
}

func draftTx(args []string) error {
	flags := newFlagSet("drafttx")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	rpcURL := flags.String("rpc", "http://127.0.0.1:9509", "pacd RPC URL")
	to := flags.String("to", "", "destination address")
	amountText := flags.String("amount", "", "amount in PAC")
	feeText := flags.String("fee", "0.0001", "fee in PAC")
	changeAddr := flags.String("change", "", "change address; defaults to first wallet address")
	sign := flags.Bool("sign", false, "sign p2pkh inputs controlled by this wallet")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("destination address is required")
	}
	amount, err := wallet.ParsePACAmount(*amountText)
	if err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	fee, err := wallet.ParsePACAmount(*feeText)
	if err != nil {
		return fmt.Errorf("fee: %w", err)
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	w, err := wallet.Load(wallet.Path(*walletDir, params.Name))
	if err != nil {
		return err
	}
	balance, err := wallet.ScanBalance(params, w, *rpcURL)
	if err != nil {
		return err
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, *to, amount, fee, *changeAddr)
	if err != nil {
		return err
	}
	if *sign {
		if err := wallet.SignDraftTx(params, w, draft); err != nil {
			return err
		}
	}
	serialized, err := draft.Tx.Serialize()
	if err != nil {
		return err
	}
	fmt.Printf("inputs: %d\n", len(draft.Tx.TxIn))
	fmt.Printf("outputs: %d\n", len(draft.Tx.TxOut))
	fmt.Printf("input_total: %s PAC\n", formatPAC(draft.InputTotal))
	fmt.Printf("output_total: %s PAC\n", formatPAC(draft.OutputTotal))
	fmt.Printf("fee: %s PAC\n", formatPAC(draft.Fee))
	fmt.Printf("change: %s PAC\n", formatPAC(draft.Change))
	fmt.Printf("change_address: %s\n", draft.ChangeAddr)
	fmt.Printf("destination: %s\n", draft.Destination)
	if *sign {
		fmt.Printf("signed_tx_hex: %s\n", hex.EncodeToString(serialized))
	} else {
		fmt.Printf("unsigned_tx_hex: %s\n", hex.EncodeToString(serialized))
	}
	for _, utxo := range draft.Selected {
		fmt.Printf("selected: %s:%d %s PAC\n", utxo.TxHash, utxo.Vout, formatPAC(utxo.Value))
	}
	return nil
}

func send(args []string) error {
	flags := newFlagSet("send")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	rpcURL := flags.String("rpc", "http://127.0.0.1:9509", "pacd RPC URL")
	to := flags.String("to", "", "destination address")
	amountText := flags.String("amount", "", "amount in PAC")
	feeText := flags.String("fee", "0.0001", "fee in PAC")
	changeAddr := flags.String("change", "", "change address; defaults to first wallet address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("destination address is required")
	}
	amount, err := wallet.ParsePACAmount(*amountText)
	if err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	fee, err := wallet.ParsePACAmount(*feeText)
	if err != nil {
		return fmt.Errorf("fee: %w", err)
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	w, err := wallet.Load(wallet.Path(*walletDir, params.Name))
	if err != nil {
		return err
	}
	balance, err := wallet.ScanBalance(params, w, *rpcURL)
	if err != nil {
		return err
	}
	draft, err := wallet.BuildDraftTx(params, w, balance, *to, amount, fee, *changeAddr)
	if err != nil {
		return err
	}
	if err := wallet.SignDraftTx(params, w, draft); err != nil {
		return err
	}
	result, err := wallet.SubmitRawTransaction(*rpcURL, draft.Tx)
	if err != nil {
		return err
	}
	fmt.Printf("accepted: %t\n", result.Accepted)
	fmt.Printf("txid: %s\n", result.TxID)
	fmt.Printf("mempool_size: %d\n", result.MempoolSize)
	fmt.Printf("fee: %s PAC\n", formatPAC(draft.Fee))
	fmt.Printf("change: %s PAC\n", formatPAC(draft.Change))
	fmt.Printf("change_address: %s\n", draft.ChangeAddr)
	fmt.Printf("destination: %s\n", draft.Destination)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet("pacwallet "+name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	return flags
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

func printKey(key wallet.Key, showPrivate bool) {
	fmt.Printf("label: %s\n", key.Label)
	fmt.Printf("address: %s\n", key.Address)
	fmt.Printf("pubkey: %s\n", key.PubKeyHex)
	if showPrivate {
		fmt.Printf("privkey: %s\n", key.PrivKeyHex)
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
	return fmt.Sprintf("%s%d.%08d", sign, whole, frac)
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, filepath.Base(os.Args[0])+":", err)
	os.Exit(1)
}
