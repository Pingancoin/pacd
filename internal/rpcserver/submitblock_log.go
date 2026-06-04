package rpcserver

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wire"
)

type SubmitBlockLogger struct {
	path string
	mu   sync.Mutex
}

type SubmitBlockLogEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	Method         string    `json:"method"`
	RemoteAddr     string    `json:"remote_addr"`
	ClientIP       string    `json:"client_ip,omitempty"`
	ForwardedFor   string    `json:"forwarded_for,omitempty"`
	UserAgent      string    `json:"user_agent,omitempty"`
	Status         int       `json:"status"`
	Accepted       bool      `json:"accepted"`
	Error          string    `json:"error,omitempty"`
	Height         uint32    `json:"height,omitempty"`
	Hash           string    `json:"hash,omitempty"`
	CoinbaseTxID   string    `json:"coinbase_txid,omitempty"`
	MinerAddress   string    `json:"miner_address,omitempty"`
	MinerReward    int64     `json:"miner_reward,omitempty"`
	ProjectAddress string    `json:"project_address,omitempty"`
	ProjectReward  int64     `json:"project_reward,omitempty"`
	BlockBytes     int       `json:"block_bytes,omitempty"`
}

func NewSubmitBlockLogger(path string) *SubmitBlockLogger {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &SubmitBlockLogger{path: path}
}

func (l *SubmitBlockLogger) Enabled() bool {
	return l != nil && l.path != ""
}

func (l *SubmitBlockLogger) Write(event SubmitBlockLogEvent) {
	if !l.Enabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()

	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	encoded = append(encoded, '\n')
	_, _ = file.Write(encoded)
}

func submitBlockLogEvent(r *http.Request) SubmitBlockLogEvent {
	forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	return SubmitBlockLogEvent{
		Timestamp:    time.Now().UTC(),
		Method:       r.Method,
		RemoteAddr:   r.RemoteAddr,
		ClientIP:     clientIPFromRequest(r),
		ForwardedFor: forwardedFor,
		UserAgent:    strings.TrimSpace(r.UserAgent()),
	}
}

func clientIPFromRequest(r *http.Request) string {
	for _, header := range []string{"X-Real-IP", "X-Forwarded-For"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value == "" {
			continue
		}
		first := strings.TrimSpace(strings.Split(value, ",")[0])
		if first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func addSubmitBlockDetails(event *SubmitBlockLogEvent, params *chaincfg.Params, block *wire.MsgBlock, blockBytes []byte) {
	if event == nil || block == nil {
		return
	}
	event.Height = block.Header.Height
	event.Hash = block.MustBlockHash().String()
	event.BlockBytes = len(blockBytes)
	if len(block.Transactions) == 0 {
		return
	}
	coinbase := block.Transactions[0]
	event.CoinbaseTxID = coinbase.MustTxHash().String()
	if len(coinbase.TxOut) > 0 {
		event.MinerReward = coinbase.TxOut[0].Value
		if addr, ok := address.AddressFromPkScript(params, coinbase.TxOut[0].PkScript); ok {
			event.MinerAddress = addr
		} else {
			event.MinerAddress = hex.EncodeToString(coinbase.TxOut[0].PkScript)
		}
	}
	if len(coinbase.TxOut) > 1 {
		event.ProjectReward = coinbase.TxOut[1].Value
		if addr, ok := address.AddressFromPkScript(params, coinbase.TxOut[1].PkScript); ok {
			event.ProjectAddress = addr
		} else {
			event.ProjectAddress = hex.EncodeToString(coinbase.TxOut[1].PkScript)
		}
	}
}

func (s *Server) writeSubmitBlockLog(event SubmitBlockLogEvent, status int, accepted bool, errText string) {
	if !s.miningLog.Enabled() {
		return
	}
	event.Status = status
	event.Accepted = accepted
	event.Error = strings.TrimSpace(errText)
	s.miningLog.Write(event)
}
