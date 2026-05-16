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
		exit(fmt.Errorf("command required: info, create, encrypt, changepassphrase, newaddress, receive, importprivkey, exportprivkey, list, pubkeys, balance, history, drafttx, send"))
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		exit(err)
	}
}

func run(command string, args []string) error {
	switch command {
	case "info":
		return info(args)
	case "create":
		return create(args)
	case "encrypt":
		return encryptWallet(args)
	case "changepassphrase":
		return changePassphrase(args)
	case "newaddress":
		return newAddress(args)
	case "receive":
		return newAddress(args)
	case "importprivkey":
		return importPrivKey(args)
	case "exportprivkey":
		return exportPrivKey(args)
	case "list":
		return list(args)
	case "pubkeys":
		return pubKeys(args)
	case "balance":
		return balance(args)
	case "history":
		return history(args)
	case "drafttx":
		return draftTx(args)
	case "send":
		return send(args)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func info(args []string) error {
	flags := newFlagSet("info")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_, path, err := walletPathFromFlags(*network, *walletDir)
	if err != nil {
		return err
	}
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	encryption := "disabled"
	if w.IsEncrypted() {
		encryption = "enabled"
	}
	fmt.Printf("wallet: %s\n", path)
	fmt.Printf("version: %d\n", w.Version)
	fmt.Printf("network: %s\n", w.Network)
	fmt.Printf("created_at: %s\n", w.CreatedAt.Format("2006-01-02T15:04:05Z"))
	fmt.Printf("encryption: %s\n", encryption)
	fmt.Printf("keys: %d\n", len(w.Keys))
	return nil
}

func walletPathFromFlags(network string, walletDir string) (*chaincfg.Params, string, error) {
	params, err := selectParams(network)
	if err != nil {
		return nil, "", err
	}
	return params, wallet.Path(walletDir, params.Name), nil
}

func create(args []string) error {
	flags := newFlagSet("create")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	passphrase := flags.String("passphrase", "", "wallet encryption passphrase; can also use PACWALLET_PASSPHRASE")
	if err := flags.Parse(args); err != nil {
		return err
	}
	params, err := selectParams(*network)
	if err != nil {
		return err
	}
	path := wallet.Path(*walletDir, params.Name)
	walletPassphrase := passphraseValue(*passphrase)
	var w *wallet.Wallet
	if walletPassphrase != "" {
		w, err = wallet.CreateEncrypted(path, params, walletPassphrase)
	} else {
		w, err = wallet.Create(path, params)
	}
	if err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	printKey(w, w.Keys[0], false, "")
	if w.IsEncrypted() {
		fmt.Println("encryption: enabled")
	} else {
		fmt.Println("warning: wallet file is not encrypted yet; protect this file")
	}
	return nil
}

func newAddress(args []string) error {
	flags := newFlagSet("newaddress")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	label := flags.String("label", "", "address label")
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
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
	if err := w.AddKeyWithPassphrase(params, *label, passphraseValue(*passphrase)); err != nil {
		return err
	}
	if err := wallet.Save(path, w); err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	printKey(w, w.Keys[len(w.Keys)-1], false, "")
	return nil
}

func encryptWallet(args []string) error {
	flags := newFlagSet("encrypt")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	passphrase := flags.String("passphrase", "", "new wallet passphrase; can also use PACWALLET_PASSPHRASE")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_, path, err := walletPathFromFlags(*network, *walletDir)
	if err != nil {
		return err
	}
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	if err := w.Encrypt(passphraseValue(*passphrase)); err != nil {
		return err
	}
	if err := wallet.Save(path, w); err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	fmt.Println("encryption: enabled")
	return nil
}

func changePassphrase(args []string) error {
	flags := newFlagSet("changepassphrase")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	oldPassphrase := flags.String("old-passphrase", "", "old wallet passphrase; can also use PACWALLET_OLD_PASSPHRASE")
	newPassphrase := flags.String("new-passphrase", "", "new wallet passphrase; can also use PACWALLET_PASSPHRASE")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_, path, err := walletPathFromFlags(*network, *walletDir)
	if err != nil {
		return err
	}
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	if err := w.ChangePassphrase(oldPassphraseValue(*oldPassphrase), passphraseValue(*newPassphrase)); err != nil {
		return err
	}
	if err := wallet.Save(path, w); err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	fmt.Println("passphrase: changed")
	return nil
}

func importPrivKey(args []string) error {
	flags := newFlagSet("importprivkey")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	label := flags.String("label", "", "address label")
	privKeyHex := flags.String("privkey", "", "32-byte private key hex")
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *privKeyHex == "" {
		return fmt.Errorf("private key is required")
	}
	params, path, err := walletPathFromFlags(*network, *walletDir)
	if err != nil {
		return err
	}
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	key, err := w.ImportPrivateKey(params, *label, *privKeyHex, passphraseValue(*passphrase))
	if err != nil {
		return err
	}
	if err := wallet.Save(path, w); err != nil {
		return err
	}
	fmt.Printf("wallet: %s\n", path)
	printKey(w, key, false, "")
	return nil
}

func exportPrivKey(args []string) error {
	flags := newFlagSet("exportprivkey")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	address := flags.String("address", "", "wallet address to export")
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" {
		return fmt.Errorf("address is required")
	}
	_, path, err := walletPathFromFlags(*network, *walletDir)
	if err != nil {
		return err
	}
	w, err := wallet.Load(path)
	if err != nil {
		return err
	}
	for _, key := range w.Keys {
		if key.Address != *address {
			continue
		}
		printKey(w, key, true, passphraseValue(*passphrase))
		return nil
	}
	return fmt.Errorf("address %s not found in wallet", *address)
}

func list(args []string) error {
	flags := newFlagSet("list")
	network := flags.String("network", "simnet", "network to use: mainnet, testnet, simnet")
	walletDir := flags.String("walletdir", wallet.DefaultDir(), "base wallet directory")
	showPrivate := flags.Bool("show-private", false, "show private keys")
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
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
		printKey(w, key, *showPrivate, passphraseValue(*passphrase))
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
	fmt.Printf("immature: %s PAC\n", formatPAC(balance.Immature))
	fmt.Printf("pending: %s PAC\n", formatPAC(balance.Pending))
	fmt.Printf("utxos: %d\n", balance.UTXOCount)
	for _, utxo := range balance.UTXOs {
		fmt.Printf("%s:%d %s PAC %s height=%d status=%s\n",
			utxo.TxHash, utxo.Vout, formatPAC(utxo.Value), utxo.Address, utxo.Height, utxoStatus(utxo))
	}
	return nil
}

func history(args []string) error {
	flags := newFlagSet("history")
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
	entries, err := wallet.ScanHistory(params, w, *rpcURL)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		status := "confirmed"
		if entry.Pending {
			status = "pending"
		}
		kind := "regular"
		if entry.Coinbase {
			kind = "coinbase"
		}
		fmt.Printf("%s height=%d status=%s type=%s received=%s PAC sent=%s PAC net=%s PAC addresses=%s\n",
			entry.TxHash, entry.Height, status, kind,
			formatPAC(entry.Received), formatPAC(entry.Sent), formatPAC(entry.Net), joinAddresses(entry.Addresses))
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
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
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
		if err := wallet.SignDraftTxWithPassphrase(params, w, draft, passphraseValue(*passphrase)); err != nil {
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
	passphrase := flags.String("passphrase", "", "wallet passphrase; can also use PACWALLET_PASSPHRASE")
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
	if err := wallet.SignDraftTxWithPassphrase(params, w, draft, passphraseValue(*passphrase)); err != nil {
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

func printKey(w *wallet.Wallet, key wallet.Key, showPrivate bool, passphrase string) {
	fmt.Printf("label: %s\n", key.Label)
	fmt.Printf("address: %s\n", key.Address)
	fmt.Printf("pubkey: %s\n", key.PubKeyHex)
	if showPrivate {
		privKeyHex, err := w.PrivateKeyHex(key, passphrase)
		if err != nil {
			exit(err)
		}
		fmt.Printf("privkey: %s\n", privKeyHex)
	}
}

func passphraseValue(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("PACWALLET_PASSPHRASE")
}

func oldPassphraseValue(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("PACWALLET_OLD_PASSPHRASE")
}

func utxoStatus(utxo wallet.UTXO) string {
	switch {
	case utxo.Pending:
		return "pending"
	case !utxo.Mature:
		return "immature"
	default:
		return "spendable"
	}
}

func joinAddresses(addresses []string) string {
	if len(addresses) == 0 {
		return "-"
	}
	result := addresses[0]
	for _, addr := range addresses[1:] {
		result += "," + addr
	}
	return result
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
