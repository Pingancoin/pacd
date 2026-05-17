package rpcserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wire"
)

type Server struct {
	chain            *blockchain.Chain
	store            *blockstore.Store
	mux              *http.ServeMux
	mu               *sync.Mutex
	mempool          []*wire.MsgTx
	onBlockConnected func(*wire.MsgBlock)
}

func New(chain *blockchain.Chain, store *blockstore.Store) *Server {
	return NewWithLock(chain, store, &sync.Mutex{})
}

func NewWithLock(chain *blockchain.Chain, store *blockstore.Store, mu *sync.Mutex) *Server {
	if mu == nil {
		mu = &sync.Mutex{}
	}
	server := &Server{
		chain: chain,
		store: store,
		mux:   http.NewServeMux(),
		mu:    mu,
	}
	server.mux.HandleFunc("/", server.handleIndex)
	server.mux.HandleFunc("/getblockcount", server.handleGetBlockCount)
	server.mux.HandleFunc("/getbestblock", server.handleGetBestBlock)
	server.mux.HandleFunc("/getblockhash/", server.handleGetBlockHash)
	server.mux.HandleFunc("/getblock/", server.handleGetBlock)
	server.mux.HandleFunc("/getrawtransaction/", server.handleGetRawTransaction)
	server.mux.HandleFunc("/getaddressutxos/", server.handleGetAddressUTXOs)
	server.mux.HandleFunc("/getmininginfo", server.handleGetMiningInfo)
	server.mux.HandleFunc("/getrawmempool", server.handleGetRawMempool)
	server.mux.HandleFunc("/submitrawtransaction", server.handleSubmitRawTransaction)
	server.mux.HandleFunc("/generate", server.handleGenerate)
	return server
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) SetBlockConnectedCallback(fn func(*wire.MsgBlock)) {
	s.onBlockConnected = fn
}

func (s *Server) ListenAndServe(ctx context.Context, listen string) error {
	httpServer := &http.Server{
		Addr:              listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, map[string]any{
		"service": "pacd rpc",
		"network": s.chain.Params().Name,
		"methods": []string{
			"/getblockcount",
			"/getbestblock",
			"/getblockhash/{height}",
			"/getblock/{hash-or-height}",
			"/getrawtransaction/{txid}",
			"/getaddressutxos/{address}",
			"/getmininginfo",
			"/getrawmempool",
			"/submitrawtransaction",
			"/generate",
		},
	})
}

func (s *Server) handleGetBlockCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]uint32{"height": s.chain.Height()})
}

func (s *Server) handleGetBestBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, blockSummary(s.chain.Tip()))
}

func (s *Server) handleGetBlockHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	heightText := strings.TrimPrefix(r.URL.Path, "/getblockhash/")
	height64, err := strconv.ParseUint(heightText, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid height")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	block, ok := s.chain.BlockByHeight(uint32(height64))
	if !ok {
		writeError(w, http.StatusNotFound, "block height not found")
		return
	}
	writeJSON(w, map[string]string{"hash": block.MustBlockHash().String()})
}

func (s *Server) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/getblock/")
	s.mu.Lock()
	defer s.mu.Unlock()
	block, err := s.lookupBlock(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if block == nil {
		writeError(w, http.StatusNotFound, "block not found")
		return
	}
	writeJSON(w, blockVerbose(s.chain.Params(), block))
}

func (s *Server) handleGetRawTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	txid := strings.TrimPrefix(r.URL.Path, "/getrawtransaction/")
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, height, pending, ok := s.lookupTransaction(txid)
	if !ok {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}
	result, err := transactionVerbose(s.chain.Params(), tx, height, pending)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleGetAddressUTXOs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	addr := strings.TrimPrefix(r.URL.Path, "/getaddressutxos/")
	s.mu.Lock()
	defer s.mu.Unlock()
	results, err := s.addressUTXOs(addr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"address": addr,
		"utxos":   results,
	})
}

func (s *Server) handleGetMiningInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	params := s.chain.Params()
	nextHeight := s.chain.Height() + 1
	nextBits := s.chain.ExpectedBits(nextHeight)
	miner, project := consensus.CalcBlockOneTimeSplit(int64(nextHeight), params)
	writeJSON(w, map[string]any{
		"network":          params.Name,
		"blocks":           s.chain.Height(),
		"bestblockhash":    s.chain.Tip().MustBlockHash().String(),
		"nextheight":       nextHeight,
		"nextbits":         fmt.Sprintf("%08x", nextBits),
		"difficulty":       consensus.DifficultyRatio(nextBits, params).FloatString(4),
		"targetspacingsec": int64(params.TargetTimePerBlock / time.Second),
		"nextsubsidy": map[string]int64{
			"miner":   miner,
			"project": project,
			"total":   miner + project,
		},
		"utxos":    s.chain.UTXOCount(),
		"mempool":  len(s.mempool),
		"datafile": s.store.Path(),
	})
}

func (s *Server) handleGetRawMempool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"size":  len(s.mempool),
		"txids": txIDs(s.mempool),
		"tx":    transactionResults(s.chain.Params(), s.mempool),
	})
}

func (s *Server) handleSubmitRawTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	txHex, err := readTxHex(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	serialized, err := hex.DecodeString(txHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transaction hex")
		return
	}
	tx, err := wire.DeserializeTx(serialized)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tx.IsCoinbase() {
		writeError(w, http.StatusBadRequest, "coinbase transactions are not accepted into mempool")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := append(append([]*wire.MsgTx(nil), s.mempool...), tx)
	if _, err := s.chain.CalcFees(candidate); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mempool = candidate
	writeJSON(w, map[string]any{
		"accepted":    true,
		"txid":        tx.MustTxHash().String(),
		"mempoolsize": len(s.mempool),
	})
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid generate request")
		return
	}
	if req.Blocks == 0 {
		req.Blocks = 1
	}
	if req.Blocks < 0 {
		writeError(w, http.StatusBadRequest, "blocks must be positive")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	params := s.chain.Params()
	if params.Name != "simnet" {
		writeError(w, http.StatusBadRequest, "RPC mining is currently available on simnet only")
		return
	}
	minerScript, err := address.DecodeAddressScript(params, req.Address)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("miner address: %v", err))
		return
	}

	hashes := make([]string, 0, req.Blocks)
	nextTime := time.Now().UTC()
	tipTime := time.Unix(s.chain.Tip().Header.Timestamp, 0).UTC()
	if !nextTime.After(tipTime) {
		nextTime = tipTime
	}
	for i := 0; i < req.Blocks; i++ {
		nextTime = nextTime.Add(params.TargetTimePerBlock)
		txs := []*wire.MsgTx(nil)
		if i == 0 && len(s.mempool) > 0 {
			txs = append(txs, s.mempool...)
		}
		block, err := mining.MineBlockWithTransactions(s.chain, minerScript, nextTime, txs, req.MaxNonce)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.chain.AddBlock(block); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.Append(block); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hashes = append(hashes, block.MustBlockHash().String())
		if s.onBlockConnected != nil {
			s.onBlockConnected(block)
		}
		if len(txs) > 0 {
			s.mempool = nil
		}
	}
	writeJSON(w, map[string]any{
		"blocks": hashes,
		"height": s.chain.Height(),
	})
}

func (s *Server) lookupBlock(id string) (*wire.MsgBlock, error) {
	if height64, err := strconv.ParseUint(id, 10, 32); err == nil {
		block, ok := s.chain.BlockByHeight(uint32(height64))
		if !ok {
			return nil, nil
		}
		return block, nil
	}
	hashBytes, err := hex.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("invalid block id")
	}
	hash, err := wire.NewHashFromBytes(hashBytes)
	if err != nil {
		return nil, err
	}
	block, ok := s.chain.BlockByHash(hash)
	if !ok {
		return nil, nil
	}
	return block, nil
}

func (s *Server) lookupTransaction(txid string) (*wire.MsgTx, uint32, bool, bool) {
	for _, tx := range s.mempool {
		if tx.MustTxHash().String() == txid {
			return tx, 0, true, true
		}
	}
	for _, block := range s.chain.Blocks() {
		for _, tx := range block.Transactions {
			if tx.MustTxHash().String() == txid {
				return tx, block.Header.Height, false, true
			}
		}
	}
	return nil, 0, false, false
}

func (s *Server) addressUTXOs(addr string) ([]addressUTXOResult, error) {
	script, err := address.DecodeAddressScript(s.chain.Params(), addr)
	if err != nil {
		return nil, err
	}
	type indexedUTXO struct {
		result addressUTXOResult
	}
	utxos := make(map[string]indexedUTXO)
	for _, block := range s.chain.Blocks() {
		for _, tx := range block.Transactions {
			for _, txIn := range tx.TxIn {
				delete(utxos, outpointKey(txIn.PreviousOutPoint.Hash.String(), txIn.PreviousOutPoint.Index))
			}
			txHash := tx.MustTxHash().String()
			for i, txOut := range tx.TxOut {
				if !bytes.Equal(txOut.PkScript, script) {
					continue
				}
				height := block.Header.Height
				utxos[outpointKey(txHash, uint32(i))] = indexedUTXO{
					result: addressUTXOResult{
						TxHash:   txHash,
						Vout:     uint32(i),
						Value:    txOut.Value,
						Height:   height,
						Coinbase: tx.IsCoinbase(),
						Mature:   !tx.IsCoinbase() || s.chain.Height()+1 >= height+s.chain.Params().CoinbaseMaturity,
					},
				}
			}
		}
	}
	for _, tx := range s.mempool {
		for _, txIn := range tx.TxIn {
			delete(utxos, outpointKey(txIn.PreviousOutPoint.Hash.String(), txIn.PreviousOutPoint.Index))
		}
		txHash := tx.MustTxHash().String()
		for i, txOut := range tx.TxOut {
			if !bytes.Equal(txOut.PkScript, script) {
				continue
			}
			utxos[outpointKey(txHash, uint32(i))] = indexedUTXO{
				result: addressUTXOResult{
					TxHash:  txHash,
					Vout:    uint32(i),
					Value:   txOut.Value,
					Pending: true,
				},
			}
		}
	}
	results := make([]addressUTXOResult, 0, len(utxos))
	for _, utxo := range utxos {
		results = append(results, utxo.result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Pending != results[j].Pending {
			return !results[i].Pending
		}
		if results[i].Height != results[j].Height {
			return results[i].Height < results[j].Height
		}
		if results[i].TxHash != results[j].TxHash {
			return results[i].TxHash < results[j].TxHash
		}
		return results[i].Vout < results[j].Vout
	})
	return results, nil
}

func outpointKey(hash string, index uint32) string {
	return fmt.Sprintf("%s:%d", hash, index)
}

type blockSummaryResult struct {
	Height     uint32 `json:"height"`
	Hash       string `json:"hash"`
	PrevHash   string `json:"prevhash"`
	Bits       string `json:"bits"`
	Nonce      uint32 `json:"nonce"`
	Time       int64  `json:"time"`
	TxCount    int    `json:"txcount"`
	MerkleRoot string `json:"merkleroot"`
}

func blockSummary(block *wire.MsgBlock) blockSummaryResult {
	return blockSummaryResult{
		Height:     block.Header.Height,
		Hash:       block.MustBlockHash().String(),
		PrevHash:   block.Header.PrevBlock.String(),
		Bits:       fmt.Sprintf("%08x", block.Header.Bits),
		Nonce:      block.Header.Nonce,
		Time:       block.Header.Timestamp,
		TxCount:    len(block.Transactions),
		MerkleRoot: block.Header.MerkleRoot.String(),
	}
}

type blockVerboseResult struct {
	blockSummaryResult
	Difficulty string              `json:"difficulty"`
	Subsidy    int64               `json:"subsidy"`
	Tx         []transactionResult `json:"tx"`
}

type transactionResult struct {
	Hash     string         `json:"hash"`
	Coinbase bool           `json:"coinbase"`
	Vin      []inputResult  `json:"vin"`
	Vout     []outputResult `json:"vout"`
}

type transactionVerboseResult struct {
	transactionResult
	Height  uint32 `json:"height"`
	Pending bool   `json:"pending"`
	Hex     string `json:"hex"`
}

type inputResult struct {
	Hash  string `json:"hash"`
	Index uint32 `json:"index"`
}

type outputResult struct {
	N        uint32 `json:"n"`
	Value    int64  `json:"value"`
	PkScript string `json:"pkscript"`
	Address  string `json:"address,omitempty"`
}

type addressUTXOResult struct {
	TxHash   string `json:"tx_hash"`
	Vout     uint32 `json:"vout"`
	Value    int64  `json:"value"`
	Height   uint32 `json:"height"`
	Coinbase bool   `json:"coinbase"`
	Mature   bool   `json:"mature"`
	Pending  bool   `json:"pending"`
}

type submitRawTransactionRequest struct {
	Hex   string `json:"hex"`
	TxHex string `json:"txhex"`
}

type generateRequest struct {
	Address  string `json:"address"`
	Blocks   int    `json:"blocks"`
	MaxNonce uint32 `json:"maxnonce"`
}

func blockVerbose(params *chaincfg.Params, block *wire.MsgBlock) blockVerboseResult {
	miner, project := consensus.CalcBlockOneTimeSplit(int64(block.Header.Height), params)
	result := blockVerboseResult{
		blockSummaryResult: blockSummary(block),
		Difficulty:         consensus.DifficultyRatio(block.Header.Bits, params).FloatString(4),
		Subsidy:            miner + project,
		Tx:                 make([]transactionResult, 0, len(block.Transactions)),
	}
	result.Tx = transactionResults(params, block.Transactions)
	return result
}

func readTxHex(r *http.Request) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		return "", err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "", fmt.Errorf("empty transaction body")
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var req submitRawTransactionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return "", fmt.Errorf("invalid JSON body")
		}
		txHex := strings.TrimSpace(req.TxHex)
		if txHex == "" {
			txHex = strings.TrimSpace(req.Hex)
		}
		if txHex == "" {
			return "", fmt.Errorf("transaction hex is required")
		}
		return txHex, nil
	}
	return string(body), nil
}

func txIDs(txs []*wire.MsgTx) []string {
	txids := make([]string, 0, len(txs))
	for _, tx := range txs {
		txids = append(txids, tx.MustTxHash().String())
	}
	return txids
}

func transactionResults(params *chaincfg.Params, txs []*wire.MsgTx) []transactionResult {
	results := make([]transactionResult, 0, len(txs))
	for _, tx := range txs {
		results = append(results, transactionSummary(params, tx))
	}
	return results
}

func transactionSummary(params *chaincfg.Params, tx *wire.MsgTx) transactionResult {
	txResult := transactionResult{
		Hash:     tx.MustTxHash().String(),
		Coinbase: tx.IsCoinbase(),
		Vin:      make([]inputResult, 0, len(tx.TxIn)),
		Vout:     make([]outputResult, 0, len(tx.TxOut)),
	}
	for _, txIn := range tx.TxIn {
		txResult.Vin = append(txResult.Vin, inputResult{
			Hash:  txIn.PreviousOutPoint.Hash.String(),
			Index: txIn.PreviousOutPoint.Index,
		})
	}
	for i, txOut := range tx.TxOut {
		output := outputResult{
			N:        uint32(i),
			Value:    txOut.Value,
			PkScript: hex.EncodeToString(txOut.PkScript),
		}
		if addr, ok := address.AddressFromPkScript(params, txOut.PkScript); ok {
			output.Address = addr
		}
		txResult.Vout = append(txResult.Vout, output)
	}
	return txResult
}

func transactionVerbose(params *chaincfg.Params, tx *wire.MsgTx, height uint32, pending bool) (transactionVerboseResult, error) {
	serialized, err := tx.Serialize()
	if err != nil {
		return transactionVerboseResult{}, err
	}
	return transactionVerboseResult{
		transactionResult: transactionSummary(params, tx),
		Height:            height,
		Pending:           pending,
		Hex:               hex.EncodeToString(serialized),
	}, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  message,
		"status": status,
	})
}
