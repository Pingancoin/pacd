package p2p

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/wire"
)

const (
	defaultHandshakeTimeout   = 10 * time.Second
	defaultIdleTimeout        = 2 * time.Minute
	defaultMaxPeers           = 32
	defaultMaxOrphans         = 128
	defaultMaxOrphanHeightGap = 288
	defaultDiscoveryInterval  = 500 * time.Millisecond
	defaultDiscoveryRetry     = 5 * time.Second
	banThreshold              = 100
	banScoreMalformed         = 100
	banScoreInvalidChain      = 50
)

type addrSource string

const (
	addrSourceDiscovered addrSource = "discovered"
	addrSourceVerified   addrSource = "verified"
	addrSourceSeed       addrSource = "seed"
	addrSourceManual     addrSource = "manual"
	addrSourceListen     addrSource = "listen"
)

type Config struct {
	Params           *chaincfg.Params
	ListenAddr       string
	Connect          []string
	SeedAddrs        []string
	MaxPeers         int
	BestHeight       func() uint32
	Chain            *blockchain.Chain
	Store            *blockstore.Store
	ChainMu          *sync.Mutex
	AddrBookPath     string
	HasTx            func(wire.Hash) bool
	TxByHash         func(wire.Hash) (*wire.MsgTx, bool)
	AcceptTx         func(*wire.MsgTx) (bool, error)
	OnBlockConnected func(*wire.MsgBlock)
	OnChainReorg     func(disconnected []*wire.MsgBlock, connected []*wire.MsgBlock)
	UserAgent        string
	Logger           *log.Logger
}

type Node struct {
	cfg      Config
	nonce    uint64
	listener net.Listener

	mu            sync.Mutex
	peers         map[string]*peerState
	addrBook      map[string]*addrBookEntry
	recentDials   map[string]time.Time
	orphans       map[wire.Hash]*wire.MsgBlock
	orphansByPrev map[wire.Hash][]wire.Hash
	sideBlocks    map[wire.Hash]*wire.MsgBlock
	sideByPrev    map[wire.Hash][]wire.Hash
	banScores     map[string]int
}

type PeerInfo struct {
	Address           string
	AdvertisedAddress string
	Inbound           bool
	BestHeight        uint32
	UserAgent         string
	ConnectedAt       time.Time
}

type AddrInfo struct {
	Address     string
	Source      string
	LastSuccess int64
	Failures    int
}

type peerState struct {
	info           PeerInfo
	conn           net.Conn
	advertisedAddr string
	writeMu        sync.Mutex
}

type addrBookEntry struct {
	Address     string     `json:"address"`
	Source      addrSource `json:"source"`
	LastSuccess int64      `json:"last_success,omitempty"`
	Failures    int        `json:"failures,omitempty"`
}

func NewNode(cfg Config) (*Node, error) {
	if cfg.Params == nil {
		return nil, fmt.Errorf("params are required")
	}
	if cfg.MaxPeers <= 0 {
		cfg.MaxPeers = defaultMaxPeers
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "/pacd:0.1.0/"
	}
	if cfg.BestHeight == nil {
		cfg.BestHeight = func() uint32 { return 0 }
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	node := &Node{
		cfg:           cfg,
		nonce:         nonce,
		peers:         make(map[string]*peerState),
		addrBook:      make(map[string]*addrBookEntry),
		recentDials:   make(map[string]time.Time),
		orphans:       make(map[wire.Hash]*wire.MsgBlock),
		orphansByPrev: make(map[wire.Hash][]wire.Hash),
		sideBlocks:    make(map[wire.Hash]*wire.MsgBlock),
		sideByPrev:    make(map[wire.Hash][]wire.Hash),
		banScores:     make(map[string]int),
	}
	if err := node.loadAddrBook(); err != nil {
		return nil, err
	}
	node.learnAddr(cfg.ListenAddr, addrSourceListen)
	for _, addr := range cfg.Connect {
		node.learnAddr(addr, addrSourceManual)
	}
	for _, addr := range cfg.SeedAddrs {
		node.learnAddr(addr, addrSourceSeed)
	}
	return node, nil
}

func (n *Node) ListenAddr() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.listener == nil {
		return n.cfg.ListenAddr
	}
	return n.listener.Addr().String()
}

func (n *Node) PeerCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.peers)
}

func (n *Node) SetTransactionCallbacks(hasTx func(wire.Hash) bool, txByHash func(wire.Hash) (*wire.MsgTx, bool), acceptTx func(*wire.MsgTx) (bool, error)) {
	n.cfg.HasTx = hasTx
	n.cfg.TxByHash = txByHash
	n.cfg.AcceptTx = acceptTx
}

func (n *Node) SetBlockConnectedCallback(fn func(*wire.MsgBlock)) {
	n.cfg.OnBlockConnected = fn
}

func (n *Node) SetChainReorgCallback(fn func(disconnected []*wire.MsgBlock, connected []*wire.MsgBlock)) {
	n.cfg.OnChainReorg = fn
}

func (n *Node) Peers() []PeerInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	peers := make([]PeerInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		peers = append(peers, peer.info)
	}
	return peers
}

func (n *Node) KnownAddressCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.addrBook)
}

func (n *Node) AddrBook() []AddrInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	addrs := make([]AddrInfo, 0, len(n.addrBook))
	for _, entry := range n.addrBook {
		addrs = append(addrs, AddrInfo{
			Address:     entry.Address,
			Source:      string(entry.Source),
			LastSuccess: entry.LastSuccess,
			Failures:    entry.Failures,
		})
	}
	slices.SortStableFunc(addrs, func(a, b AddrInfo) int {
		if a.Address < b.Address {
			return -1
		}
		if a.Address > b.Address {
			return 1
		}
		return 0
	})
	return addrs
}

func (n *Node) Start(ctx context.Context) error {
	if n.cfg.ListenAddr != "" {
		listener, err := net.Listen("tcp", n.cfg.ListenAddr)
		if err != nil {
			return err
		}
		n.mu.Lock()
		n.listener = listener
		n.mu.Unlock()
		n.learnAddr(listener.Addr().String(), addrSourceListen)
		go n.acceptLoop(ctx, listener)
	}

	for _, addr := range n.cfg.Connect {
		addr := addr
		go n.connectLoop(ctx, addr)
	}
	go n.discoveryLoop(ctx)

	<-ctx.Done()
	n.mu.Lock()
	listener := n.listener
	n.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	return nil
}

func (n *Node) DialOnce(ctx context.Context, addr string) error {
	go func() {
		if err := n.connectAndHandle(ctx, addr, false); err != nil {
			n.logf("p2p connect %s failed: %v", addr, err)
		}
	}()
	return nil
}

func (n *Node) connectAndHandle(ctx context.Context, addr string, static bool) error {
	dialer := &net.Dialer{Timeout: defaultHandshakeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	n.handleConn(ctx, conn, false, static)
	return nil
}

func (n *Node) acceptLoop(ctx context.Context, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			n.logf("p2p accept error: %v", err)
			continue
		}
		go n.handleConn(ctx, conn, true, false)
	}
}

func (n *Node) connectLoop(ctx context.Context, addr string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := n.connectAndHandle(ctx, addr, true); err != nil {
			n.logf("p2p connect %s failed: %v", addr, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (n *Node) handleConn(ctx context.Context, conn net.Conn, inbound bool, static bool) {
	defer conn.Close()
	addr := conn.RemoteAddr().String()
	if !n.reservePeer(addr, inbound, static) {
		n.logf("p2p rejecting %s: max peers reached", addr)
		return
	}
	defer n.removePeer(addr)

	remoteVersion, err := n.handshake(conn, inbound)
	if err != nil {
		n.logf("p2p handshake %s failed: %v", addr, err)
		return
	}
	advertisedAddr := normalizeAddr(remoteVersion.ListenAddr)
	n.updatePeer(addr, PeerInfo{
		Address:           addr,
		AdvertisedAddress: advertisedAddr,
		Inbound:           inbound,
		BestHeight:        remoteVersion.BestHeight,
		UserAgent:         remoteVersion.UserAgent,
		ConnectedAt:       time.Now().UTC(),
	}, conn, advertisedAddr)
	if advertisedAddr != "" {
		n.learnAddr(advertisedAddr, addrSourceVerified)
	}
	if !inbound {
		n.learnAddr(addr, addrSourceVerified)
		n.recordDiscoveryResult(advertisedAddr, true)
		n.recordDiscoveryResult(addr, true)
	}
	n.logf("p2p connected %s inbound=%t height=%d agent=%s", addr, inbound, remoteVersion.BestHeight, remoteVersion.UserAgent)
	if remoteVersion.BestHeight > n.bestHeight() {
		if err := n.sendGetHeaders(addr); err != nil {
			n.logf("p2p getheaders %s failed: %v", addr, err)
			return
		}
	}
	if err := n.sendAddr(addr); err != nil {
		n.logf("p2p addr %s failed: %v", addr, err)
		return
	}
	n.readLoop(ctx, conn, addr)
}

func (n *Node) handshake(conn net.Conn, inbound bool) (Version, error) {
	if err := conn.SetDeadline(time.Now().Add(defaultHandshakeTimeout)); err != nil {
		return Version{}, err
	}
	localVersion := NewVersion(n.cfg.Params.Name, n.cfg.Params.GenesisHash, n.bestHeight(), n.nonce, normalizeAddr(n.ListenAddr()), n.cfg.UserAgent)
	localPayload, err := localVersion.Serialize()
	if err != nil {
		return Version{}, err
	}

	var remote Version
	if inbound {
		remote, err = n.readVersion(conn)
		if err != nil {
			return Version{}, err
		}
		if err := WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandVersion, Payload: localPayload}); err != nil {
			return Version{}, err
		}
		if err := n.expectCommand(conn, CommandVerAck); err != nil {
			return Version{}, err
		}
		if err := WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandVerAck}); err != nil {
			return Version{}, err
		}
	} else {
		if err := WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandVersion, Payload: localPayload}); err != nil {
			return Version{}, err
		}
		remote, err = n.readVersion(conn)
		if err != nil {
			return Version{}, err
		}
		if err := WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandVerAck}); err != nil {
			return Version{}, err
		}
		if err := n.expectCommand(conn, CommandVerAck); err != nil {
			return Version{}, err
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return Version{}, err
	}
	return remote, nil
}

func (n *Node) readVersion(conn net.Conn) (Version, error) {
	msg, err := ReadMessage(conn, n.cfg.Params.NetworkMagic)
	if err != nil {
		return Version{}, err
	}
	if msg.Command != CommandVersion {
		return Version{}, fmt.Errorf("expected version, got %q", msg.Command)
	}
	remote, err := DeserializeVersion(msg.Payload)
	if err != nil {
		return Version{}, err
	}
	if remote.ProtocolVersion != ProtocolVersion {
		return Version{}, fmt.Errorf("protocol version %d is unsupported", remote.ProtocolVersion)
	}
	if remote.Network != n.cfg.Params.Name {
		return Version{}, fmt.Errorf("network %q does not match %q", remote.Network, n.cfg.Params.Name)
	}
	if remote.GenesisHash != n.cfg.Params.GenesisHash {
		return Version{}, fmt.Errorf("genesis hash mismatch")
	}
	if remote.Nonce == n.nonce {
		return Version{}, fmt.Errorf("self connection rejected")
	}
	return remote, nil
}

func (n *Node) expectCommand(conn net.Conn, command string) error {
	msg, err := ReadMessage(conn, n.cfg.Params.NetworkMagic)
	if err != nil {
		return err
	}
	if msg.Command != command {
		return fmt.Errorf("expected %s, got %q", command, msg.Command)
	}
	return nil
}

func (n *Node) readLoop(ctx context.Context, conn net.Conn, addr string) {
	for {
		if err := conn.SetReadDeadline(time.Now().Add(defaultIdleTimeout)); err != nil {
			return
		}
		msg, err := ReadMessage(conn, n.cfg.Params.NetworkMagic)
		if err != nil {
			if shouldBanReadError(err) {
				n.addBanScore(addr, banScoreMalformed, "read failure")
			}
			return
		}
		switch msg.Command {
		case CommandPing:
			_ = n.writePeerMessage(addr, Message{Command: CommandPong, Payload: msg.Payload})
		case CommandPong:
		case CommandGetHeaders:
			if err := n.handleGetHeaders(addr, msg.Payload); err != nil {
				n.addBanScore(addr, banScoreMalformed, "getheaders")
				n.logf("p2p getheaders from %s failed: %v", addr, err)
				return
			}
		case CommandHeaders:
			if err := n.handleHeaders(addr, msg.Payload); err != nil {
				n.addBanScore(addr, banScoreInvalidChain, "headers")
				n.logf("p2p headers from %s failed: %v", addr, err)
				return
			}
		case CommandGetBlocks:
			if err := n.handleGetBlocks(addr, msg.Payload); err != nil {
				n.logf("p2p getblocks from %s failed: %v", addr, err)
				return
			}
		case CommandInv:
			if err := n.handleInv(addr, msg.Payload); err != nil {
				n.addBanScore(addr, banScoreMalformed, "inv")
				n.logf("p2p inv from %s failed: %v", addr, err)
				return
			}
		case CommandGetData:
			if err := n.handleGetData(addr, msg.Payload); err != nil {
				n.logf("p2p getdata from %s failed: %v", addr, err)
				return
			}
		case CommandTx:
			if err := n.handleTx(addr, msg.Payload); err != nil {
				n.addBanScore(addr, banScoreInvalidChain, "tx")
				n.logf("p2p tx from %s failed: %v", addr, err)
				return
			}
		case CommandGetAddr:
			if err := n.sendAddr(addr); err != nil {
				n.logf("p2p getaddr from %s failed: %v", addr, err)
				return
			}
		case CommandAddr:
			if err := n.handleAddr(msg.Payload); err != nil {
				n.addBanScore(addr, banScoreMalformed, "addr")
				n.logf("p2p addr from %s failed: %v", addr, err)
				return
			}
		case CommandBlock:
			if err := n.handleBlock(addr, msg.Payload); err != nil {
				n.addBanScore(addr, banScoreInvalidChain, "block")
				n.logf("p2p block from %s failed: %v", addr, err)
				return
			}
		default:
			n.logf("p2p ignored command %q from %s", msg.Command, conn.RemoteAddr())
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (n *Node) handleGetHeaders(addr string, payload []byte) error {
	req, err := DeserializeGetHeaders(payload)
	if err != nil {
		return err
	}
	headers := n.headersAfter(req.StartHeight)
	serialized, err := (Headers{Headers: headers}).Serialize()
	if err != nil {
		return err
	}
	return n.writePeerMessage(addr, Message{Command: CommandHeaders, Payload: serialized})
}

func (n *Node) handleHeaders(addr string, payload []byte) error {
	headers, err := DeserializeHeaders(payload)
	if err != nil {
		return err
	}
	if len(headers.Headers) == 0 {
		return nil
	}
	hashes, err := n.validateHeaderChain(headers.Headers)
	if err != nil {
		return err
	}
	if len(hashes) == 0 {
		return nil
	}
	if len(hashes) > MaxBlocksPerRequest {
		hashes = hashes[:MaxBlocksPerRequest]
	}
	serialized, err := (GetBlocks{Hashes: hashes}).Serialize()
	if err != nil {
		return err
	}
	return n.writePeerMessage(addr, Message{Command: CommandGetBlocks, Payload: serialized})
}

func (n *Node) handleGetBlocks(addr string, payload []byte) error {
	req, err := DeserializeGetBlocks(payload)
	if err != nil {
		return err
	}
	for _, hash := range req.Hashes {
		block, ok := n.blockByHash(hash)
		if !ok {
			continue
		}
		serialized, err := block.Serialize()
		if err != nil {
			return err
		}
		if err := n.writePeerMessage(addr, Message{Command: CommandBlock, Payload: serialized}); err != nil {
			return err
		}
	}
	return nil
}

func (n *Node) handleInv(addr string, payload []byte) error {
	inv, err := DeserializeInventory(payload)
	if err != nil {
		return err
	}
	requests := make([]InvVector, 0, len(inv.Items))
	for _, item := range inv.Items {
		switch item.Type {
		case InvTypeBlock:
			if n.hasBlock(item.Hash) {
				continue
			}
		case InvTypeTx:
			if n.hasTx(item.Hash) {
				continue
			}
		default:
			continue
		}
		requests = append(requests, item)
	}
	if len(requests) == 0 {
		return nil
	}
	serialized, err := (Inventory{Items: requests}).Serialize()
	if err != nil {
		return err
	}
	return n.writePeerMessage(addr, Message{Command: CommandGetData, Payload: serialized})
}

func (n *Node) handleGetData(addr string, payload []byte) error {
	req, err := DeserializeInventory(payload)
	if err != nil {
		return err
	}
	for _, item := range req.Items {
		switch item.Type {
		case InvTypeBlock:
			block, ok := n.blockByHash(item.Hash)
			if !ok {
				continue
			}
			serialized, err := block.Serialize()
			if err != nil {
				return err
			}
			if err := n.writePeerMessage(addr, Message{Command: CommandBlock, Payload: serialized}); err != nil {
				return err
			}
		case InvTypeTx:
			tx, ok := n.txByHash(item.Hash)
			if !ok {
				continue
			}
			serialized, err := tx.Serialize()
			if err != nil {
				return err
			}
			if err := n.writePeerMessage(addr, Message{Command: CommandTx, Payload: serialized}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *Node) handleAddr(payload []byte) error {
	list, err := DeserializeAddrList(payload)
	if err != nil {
		return err
	}
	for _, addr := range list.Addrs {
		n.learnAddr(addr, addrSourceDiscovered)
	}
	return nil
}

func (n *Node) handleBlock(addr string, payload []byte) error {
	block, err := wire.DeserializeBlock(payload)
	if err != nil {
		return err
	}
	connected, connectedBlocks, disconnectedBlocks, err := n.connectBlock(block)
	if err != nil {
		return err
	}
	if connected {
		n.logf("p2p connected block height=%d hash=%s", block.Header.Height, block.MustBlockHash())
		if len(disconnectedBlocks) > 0 && n.cfg.OnChainReorg != nil {
			n.cfg.OnChainReorg(disconnectedBlocks, connectedBlocks)
		} else {
			for _, connectedBlock := range connectedBlocks {
				if n.cfg.OnBlockConnected != nil {
					n.cfg.OnBlockConnected(connectedBlock)
				}
			}
		}
		for _, connectedBlock := range connectedBlocks {
			n.broadcastInventory(connectedBlock.MustBlockHash(), addr)
		}
	}
	if info, ok := n.peerInfo(addr); ok && info.BestHeight > n.bestHeight() {
		return n.sendGetHeaders(addr)
	}
	return nil
}

func (n *Node) handleTx(addr string, payload []byte) error {
	tx, err := wire.DeserializeTx(payload)
	if err != nil {
		return err
	}
	if tx.IsCoinbase() {
		return fmt.Errorf("coinbase transactions are not accepted into mempool")
	}
	if n.cfg.AcceptTx == nil {
		return nil
	}
	accepted, err := n.cfg.AcceptTx(tx)
	if err != nil {
		return err
	}
	if accepted {
		n.logf("p2p accepted tx %s", tx.MustTxHash())
		n.broadcastInv([]InvVector{{Type: InvTypeTx, Hash: tx.MustTxHash()}}, addr)
	}
	return nil
}

func (n *Node) sendGetHeaders(addr string) error {
	payload, err := (GetHeaders{StartHeight: n.bestHeight()}).Serialize()
	if err != nil {
		return err
	}
	return n.writePeerMessage(addr, Message{Command: CommandGetHeaders, Payload: payload})
}

func (n *Node) headersAfter(startHeight uint32) []wire.BlockHeader {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return nil
	}
	blocks := n.cfg.Chain.Blocks()
	if int(startHeight)+1 >= len(blocks) {
		return nil
	}
	end := len(blocks)
	if end > int(startHeight)+1+MaxHeadersPerMessage {
		end = int(startHeight) + 1 + MaxHeadersPerMessage
	}
	headers := make([]wire.BlockHeader, 0, end-int(startHeight)-1)
	for _, block := range blocks[startHeight+1 : end] {
		headers = append(headers, block.Header)
	}
	return headers
}

func (n *Node) validateHeaderChain(headers []wire.BlockHeader) ([]wire.Hash, error) {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return nil, nil
	}
	expectedHeight := n.cfg.Chain.Height() + 1
	if len(headers) > 0 && !chaincfg.MiningOpen(n.cfg.Params, time.Now().UTC()) {
		return nil, fmt.Errorf("%s mining opens at %s", n.cfg.Params.Name, chaincfg.MiningStartTimeText(n.cfg.Params))
	}
	prevHash := n.cfg.Chain.Tip().MustBlockHash()
	prevTime := n.cfg.Chain.Tip().Header.Timestamp
	now := time.Now().UTC()
	hashes := make([]wire.Hash, 0, len(headers))
	for _, header := range headers {
		if header.Height < expectedHeight {
			continue
		}
		if header.Height != expectedHeight {
			return nil, fmt.Errorf("header height %d does not extend expected %d", header.Height, expectedHeight)
		}
		if header.PrevBlock != prevHash {
			return nil, fmt.Errorf("header previous hash mismatch at height %d", header.Height)
		}
		if err := consensus.CheckBlockTimestamp(n.cfg.Params, prevTime, header.Timestamp, now); err != nil {
			return nil, fmt.Errorf("header timestamp at height %d: %w", header.Height, err)
		}
		expectedBits := consensus.CalcASERTNextBits(
			n.cfg.Params.GenesisBlock.Header.Bits,
			n.cfg.Params.GenesisBlock.Header.Timestamp,
			prevTime,
			int64(header.Height),
			n.cfg.Params,
		)
		if header.Bits != expectedBits {
			return nil, fmt.Errorf("header bits %08x do not match expected %08x at height %d", header.Bits, expectedBits, header.Height)
		}
		if err := consensus.CheckProofOfWork(&header, n.cfg.Params); err != nil {
			return nil, err
		}
		hash := header.MustBlockHash()
		hashes = append(hashes, hash)
		prevHash = hash
		prevTime = header.Timestamp
		expectedHeight++
	}
	return hashes, nil
}

func (n *Node) blockByHash(hash wire.Hash) (*wire.MsgBlock, bool) {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return nil, false
	}
	return n.cfg.Chain.BlockByHash(hash)
}

func (n *Node) connectBlock(block *wire.MsgBlock) (bool, []*wire.MsgBlock, []*wire.MsgBlock, error) {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return false, nil, nil, nil
	}
	if block.Header.Height <= n.cfg.Chain.Height() {
		if existing, ok := n.cfg.Chain.BlockByHeight(block.Header.Height); ok && existing.MustBlockHash() == block.MustBlockHash() {
			return false, nil, nil, nil
		}
		return n.connectSideBlockLocked(block)
	}
	tip := n.cfg.Chain.Tip()
	if block.Header.PrevBlock != tip.MustBlockHash() || block.Header.Height != n.cfg.Chain.Height()+1 {
		if n.knowsSideOrMainPrevLocked(block.Header.PrevBlock) {
			return n.connectSideBlockLocked(block)
		}
		if err := n.validateOrphanCandidateLocked(block); err != nil {
			return false, nil, nil, err
		}
		n.addOrphanLocked(block)
		return false, nil, nil, nil
	}
	if err := n.cfg.Chain.ValidateBlock(block); err != nil {
		return false, nil, nil, err
	}
	if n.cfg.Store != nil {
		if err := n.cfg.Store.Append(block); err != nil {
			return false, nil, nil, err
		}
	}
	if err := n.cfg.Chain.AddBlock(block); err != nil {
		return false, nil, nil, err
	}
	connected := []*wire.MsgBlock{block}
	connected = append(connected, n.connectOrphansLocked(block.MustBlockHash())...)
	return true, connected, nil, nil
}

func (n *Node) bestHeight() uint32 {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain != nil {
		return n.cfg.Chain.Height()
	}
	return n.cfg.BestHeight()
}

func (n *Node) lockChain() {
	if n.cfg.ChainMu != nil {
		n.cfg.ChainMu.Lock()
	}
}

func (n *Node) unlockChain() {
	if n.cfg.ChainMu != nil {
		n.cfg.ChainMu.Unlock()
	}
}

func (n *Node) reservePeer(addr string, inbound bool, static bool) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.peers[addr]; ok {
		return false
	}
	if n.banScores[banKey(addr)] >= banThreshold {
		return false
	}
	isStatic := static || n.isStaticPeerLocked(addr)
	if n.hasPeerForHostLocked(addr) {
		if !isStatic || n.hasActivePeerForHostLocked(addr) {
			return false
		}
		n.removePendingPeersForHostLocked(addr)
	}
	activePeers := n.activePeerCountLocked()
	if activePeers >= n.cfg.MaxPeers && (inbound || !isStatic) {
		return false
	}
	if !isStatic && n.pendingPeerCountLocked() >= maxPendingPeers(n.cfg.MaxPeers) {
		return false
	}
	n.peers[addr] = &peerState{info: PeerInfo{Address: addr}}
	return true
}

func (n *Node) updatePeer(addr string, info PeerInfo, conn net.Conn, advertisedAddr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	peer, ok := n.peers[addr]
	if !ok {
		peer = &peerState{}
		n.peers[addr] = peer
	}
	peer.info = info
	peer.conn = conn
	peer.advertisedAddr = advertisedAddr
}

func (n *Node) removePeer(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.peers, addr)
}

func (n *Node) activePeerCountLocked() int {
	count := 0
	for _, peer := range n.peers {
		if peer.conn != nil {
			count++
		}
	}
	return count
}

func (n *Node) pendingPeerCountLocked() int {
	count := 0
	for _, peer := range n.peers {
		if peer.conn == nil {
			count++
		}
	}
	return count
}

func (n *Node) hasActivePeerForHostLocked(addr string) bool {
	host := banKey(addr)
	if host == "" || isLoopbackHost(host) {
		return false
	}
	for peerAddr, peer := range n.peers {
		if peer.conn != nil && banKey(peerAddr) == host {
			return true
		}
		if peer.conn != nil && peer.advertisedAddr != "" && banKey(peer.advertisedAddr) == host {
			return true
		}
	}
	return false
}

func (n *Node) removePendingPeersForHostLocked(addr string) {
	host := banKey(addr)
	if host == "" || isLoopbackHost(host) {
		return
	}
	for peerAddr, peer := range n.peers {
		if peer.conn != nil {
			continue
		}
		if banKey(peerAddr) == host || (peer.advertisedAddr != "" && banKey(peer.advertisedAddr) == host) {
			delete(n.peers, peerAddr)
		}
	}
}

func maxPendingPeers(maxPeers int) int {
	if maxPeers < 1 {
		return 1
	}
	return maxPeers * 2
}

func (n *Node) peerInfo(addr string) (PeerInfo, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	peer, ok := n.peers[addr]
	if !ok {
		return PeerInfo{}, false
	}
	return peer.info, true
}

func (n *Node) writePeerMessage(addr string, msg Message) error {
	n.mu.Lock()
	peer, ok := n.peers[addr]
	n.mu.Unlock()
	if !ok || peer.conn == nil {
		return fmt.Errorf("peer %s is not connected", addr)
	}
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	return WriteMessage(peer.conn, n.cfg.Params.NetworkMagic, msg)
}

func (n *Node) broadcastInv(items []InvVector, exceptAddr string) {
	serialized, err := (Inventory{Items: items}).Serialize()
	if err != nil {
		n.logf("p2p inventory serialize failed: %v", err)
		return
	}
	n.mu.Lock()
	peers := make([]*peerState, 0, len(n.peers))
	for addr, peer := range n.peers {
		if addr == exceptAddr || peer.conn == nil {
			continue
		}
		peers = append(peers, peer)
	}
	n.mu.Unlock()
	for _, peer := range peers {
		peer.writeMu.Lock()
		err := WriteMessage(peer.conn, n.cfg.Params.NetworkMagic, Message{Command: CommandInv, Payload: serialized})
		peer.writeMu.Unlock()
		if err != nil {
			n.logf("p2p inventory relay failed: %v", err)
		}
	}
}

func (n *Node) broadcastInventory(hash wire.Hash, exceptAddr string) {
	n.broadcastInv([]InvVector{{Type: InvTypeBlock, Hash: hash}}, exceptAddr)
}

func (n *Node) sendAddr(addr string) error {
	serialized, err := (AddrList{Addrs: n.knownAddresses()}).Serialize()
	if err != nil {
		return err
	}
	return n.writePeerMessage(addr, Message{Command: CommandAddr, Payload: serialized})
}

func (n *Node) knownAddresses() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	addrs := make([]string, 0, len(n.addrBook))
	for addr := range n.addrBook {
		addrs = append(addrs, addr)
	}
	return addrs
}

func (n *Node) rememberAddr(addr string) {
	n.learnAddr(addr, addrSourceDiscovered)
}

func (n *Node) learnAddr(addr string, source addrSource) {
	addr = normalizeAddr(addr)
	if addr == "" {
		return
	}
	n.mu.Lock()
	updated := false
	entry, ok := n.addrBook[addr]
	if !ok {
		n.addrBook[addr] = &addrBookEntry{Address: addr, Source: source}
		updated = true
	} else if betterAddrSource(source, entry.Source) {
		entry.Source = source
		updated = true
	}
	n.mu.Unlock()
	if updated {
		n.persistAddrBook()
	}
}

func betterAddrSource(next, current addrSource) bool {
	return addrSourceRank(next) > addrSourceRank(current)
}

func addrSourceRank(source addrSource) int {
	switch source {
	case addrSourceVerified:
		return 4
	case addrSourceManual:
		return 3
	case addrSourceSeed:
		return 2
	case addrSourceDiscovered:
		return 1
	case addrSourceListen:
		return 0
	default:
		return 0
	}
}

func (n *Node) addrBookInfo(addr string) (addrBookEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	entry, ok := n.addrBook[normalizeAddr(addr)]
	if !ok {
		return addrBookEntry{}, false
	}
	return *entry, true
}

func (n *Node) updateAddrBookEntry(addr string, fn func(*addrBookEntry) bool) {
	addr = normalizeAddr(addr)
	if addr == "" {
		return
	}
	n.mu.Lock()
	entry, ok := n.addrBook[addr]
	if !ok {
		n.mu.Unlock()
		return
	}
	updated := fn(entry)
	n.mu.Unlock()
	if updated {
		n.persistAddrBook()
	}
}

func (n *Node) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultDiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.discoveryStep(ctx)
		}
	}
}

func (n *Node) discoveryStep(ctx context.Context) {
	if ctx.Err() != nil || n.PeerCount() >= n.cfg.MaxPeers {
		return
	}
	for _, addr := range n.discoveryCandidates() {
		addr := addr
		go func() {
			if err := n.connectAndHandle(ctx, addr, false); err != nil && ctx.Err() == nil {
				n.recordDiscoveryResult(addr, false)
				n.logf("p2p discovered connect %s failed: %v", addr, err)
			}
		}()
		return
	}
}

func (n *Node) discoveryCandidates() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	self := normalizeAddr(n.cfg.ListenAddr)
	if n.listener != nil {
		self = normalizeAddr(n.listener.Addr().String())
	}
	candidates := make([]string, 0, len(n.addrBook))
	for addr, entry := range n.addrBook {
		if addr == "" || addr == self {
			continue
		}
		if _, ok := n.peers[addr]; ok {
			continue
		}
		if n.hasPeerForHostLocked(addr) {
			continue
		}
		if n.hasPeerForAdvertisedAddr(addr) {
			continue
		}
		if n.isStaticPeerLocked(addr) {
			continue
		}
		if n.banScores[banKey(addr)] >= banThreshold {
			continue
		}
		if last, ok := n.recentDials[addr]; ok && now.Sub(last) < n.discoveryRetryDelay(entry.Failures) {
			continue
		}
		n.recentDials[addr] = now
		candidates = append(candidates, addr)
	}
	slices.SortStableFunc(candidates, func(a, b string) int {
		left := n.addrBook[a]
		right := n.addrBook[b]
		if cmp := addrSourceRank(right.Source) - addrSourceRank(left.Source); cmp != 0 {
			return cmp
		}
		if left.LastSuccess != right.LastSuccess {
			if left.LastSuccess > right.LastSuccess {
				return -1
			}
			return 1
		}
		if left.Failures != right.Failures {
			return left.Failures - right.Failures
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	return candidates
}

func (n *Node) hasPeerForAdvertisedAddr(addr string) bool {
	for _, peer := range n.peers {
		if peer.advertisedAddr == addr {
			return true
		}
	}
	return false
}

func (n *Node) hasPeerForHostLocked(addr string) bool {
	host := banKey(addr)
	if host == "" || isLoopbackHost(host) {
		return false
	}
	for peerAddr := range n.peers {
		if banKey(peerAddr) == host {
			return true
		}
	}
	for _, peer := range n.peers {
		if peer.advertisedAddr != "" && banKey(peer.advertisedAddr) == host {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (n *Node) isStaticPeer(addr string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isStaticPeerLocked(addr)
}

func (n *Node) isStaticPeerLocked(addr string) bool {
	for _, peer := range n.cfg.Connect {
		if normalizeAddr(peer) == addr {
			return true
		}
	}
	return false
}

func (n *Node) recordDiscoveryResult(addr string, success bool) {
	addr = normalizeAddr(addr)
	if addr == "" {
		return
	}
	n.updateAddrBookEntry(addr, func(entry *addrBookEntry) bool {
		now := time.Now().UTC().Unix()
		if success {
			updated := entry.Source != addrSourceVerified || entry.LastSuccess != now || entry.Failures != 0
			entry.Source = addrSourceVerified
			entry.LastSuccess = now
			entry.Failures = 0
			return updated
		}
		entry.Failures++
		return true
	})
}

func (n *Node) discoveryRetryDelay(failures int) time.Duration {
	if failures <= 0 {
		return defaultDiscoveryRetry
	}
	delay := defaultDiscoveryRetry
	for i := 1; i < failures && delay < time.Minute; i++ {
		delay *= 2
	}
	if delay > time.Minute {
		delay = time.Minute
	}
	return delay
}

func (n *Node) loadAddrBook() error {
	if n.cfg.AddrBookPath == "" {
		return nil
	}
	data, err := os.ReadFile(n.cfg.AddrBookPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var addrs []addrBookEntry
	if err := json.Unmarshal(data, &addrs); err != nil {
		var legacy []string
		if err := json.Unmarshal(data, &legacy); err != nil {
			return fmt.Errorf("decode addrbook %s: %w", n.cfg.AddrBookPath, err)
		}
		for _, addr := range legacy {
			addr = normalizeAddr(addr)
			if addr == "" {
				continue
			}
			n.addrBook[addr] = &addrBookEntry{Address: addr, Source: addrSourceDiscovered}
		}
		return nil
	}
	for _, entry := range addrs {
		addr := normalizeAddr(entry.Address)
		if addr == "" {
			continue
		}
		entry.Address = addr
		if entry.Source == "" {
			entry.Source = addrSourceDiscovered
		}
		copied := entry
		n.addrBook[addr] = &copied
	}
	return nil
}

func (n *Node) persistAddrBook() {
	if n.cfg.AddrBookPath == "" {
		return
	}
	addrs := n.addrBookEntries()
	if err := os.MkdirAll(filepath.Dir(n.cfg.AddrBookPath), 0o755); err != nil {
		n.logf("p2p addrbook mkdir failed: %v", err)
		return
	}
	data, err := json.MarshalIndent(addrs, "", "  ")
	if err != nil {
		n.logf("p2p addrbook encode failed: %v", err)
		return
	}
	tmp := n.cfg.AddrBookPath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		n.logf("p2p addrbook write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, n.cfg.AddrBookPath); err != nil {
		n.logf("p2p addrbook replace failed: %v", err)
	}
}

func (n *Node) addrBookEntries() []addrBookEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	addrs := make([]addrBookEntry, 0, len(n.addrBook))
	for _, entry := range n.addrBook {
		addrs = append(addrs, *entry)
	}
	slices.SortStableFunc(addrs, func(a, b addrBookEntry) int {
		if a.Address < b.Address {
			return -1
		}
		if a.Address > b.Address {
			return 1
		}
		return 0
	})
	return addrs
}

func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || port == "" || port == "0" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return ""
	}
	return net.JoinHostPort(host, port)
}

func banKey(addr string) string {
	addr = strings.TrimSpace(addr)
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return addr
	}
	return host
}

func (n *Node) hasBlock(hash wire.Hash) bool {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain != nil {
		if _, ok := n.cfg.Chain.BlockByHash(hash); ok {
			return true
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.orphans[hash]
	return ok
}

func (n *Node) hasTx(hash wire.Hash) bool {
	if n.cfg.HasTx == nil {
		return false
	}
	return n.cfg.HasTx(hash)
}

func (n *Node) txByHash(hash wire.Hash) (*wire.MsgTx, bool) {
	if n.cfg.TxByHash == nil {
		return nil, false
	}
	return n.cfg.TxByHash(hash)
}

func (n *Node) addOrphanLocked(block *wire.MsgBlock) {
	hash := block.MustBlockHash()
	if _, ok := n.orphans[hash]; ok {
		return
	}
	if len(n.orphans) >= defaultMaxOrphans {
		for orphanHash, orphan := range n.orphans {
			delete(n.orphans, orphanHash)
			prev := orphan.Header.PrevBlock
			n.orphansByPrev[prev] = removeHash(n.orphansByPrev[prev], orphanHash)
			if len(n.orphansByPrev[prev]) == 0 {
				delete(n.orphansByPrev, prev)
			}
			break
		}
	}
	n.orphans[hash] = block
	prev := block.Header.PrevBlock
	n.orphansByPrev[prev] = append(n.orphansByPrev[prev], hash)
}

func (n *Node) connectSideBlockLocked(block *wire.MsgBlock) (bool, []*wire.MsgBlock, []*wire.MsgBlock, error) {
	if err := n.validateSideCandidateLocked(block); err != nil {
		n.logf("p2p ignored invalid side block height=%d hash=%s: %v", block.Header.Height, block.MustBlockHash(), err)
		return false, nil, nil, nil
	}
	hash := block.MustBlockHash()
	if _, ok := n.sideBlocks[hash]; !ok {
		n.addSideBlockLocked(block)
	}
	connected, branch, disconnected, err := n.tryReorganizeLocked(hash)
	if err != nil {
		return false, nil, nil, err
	}
	return connected, branch, disconnected, nil
}

func (n *Node) addSideBlockLocked(block *wire.MsgBlock) {
	hash := block.MustBlockHash()
	if _, ok := n.sideBlocks[hash]; ok {
		return
	}
	n.sideBlocks[hash] = block
	prev := block.Header.PrevBlock
	n.sideByPrev[prev] = append(n.sideByPrev[prev], hash)
}

func (n *Node) tryReorganizeLocked(tipHash wire.Hash) (bool, []*wire.MsgBlock, []*wire.MsgBlock, error) {
	branch, ok := n.sideBranchLocked(tipHash)
	if !ok || len(branch) == 0 {
		return false, nil, nil, nil
	}
	if branch[len(branch)-1].Header.Height <= n.cfg.Chain.Height() {
		return false, nil, nil, nil
	}
	disconnected := n.disconnectedBlocksForBranchLocked(branch)
	reorganizedBlocks, ok, err := n.cfg.Chain.ReorganizedBlocks(branch)
	if err != nil || !ok {
		return false, nil, nil, err
	}
	if n.cfg.Store != nil {
		if err := n.cfg.Store.Replace(reorganizedBlocks); err != nil {
			return false, nil, nil, err
		}
	}
	ok, err = n.cfg.Chain.Reorganize(branch)
	if err != nil || !ok {
		return false, nil, nil, err
	}
	n.pruneSideBranchLocked(branch)
	n.logf("p2p reorganized to height=%d hash=%s", n.cfg.Chain.Height(), n.cfg.Chain.Tip().MustBlockHash())
	return true, branch, disconnected, nil
}

func (n *Node) sideBranchLocked(tipHash wire.Hash) ([]*wire.MsgBlock, bool) {
	var reversed []*wire.MsgBlock
	seen := make(map[wire.Hash]struct{})
	for {
		if _, ok := seen[tipHash]; ok {
			return nil, false
		}
		seen[tipHash] = struct{}{}
		block, ok := n.sideBlocks[tipHash]
		if !ok {
			return nil, false
		}
		reversed = append(reversed, block)
		if _, ok := n.cfg.Chain.BlockByHash(block.Header.PrevBlock); ok {
			break
		}
		tipHash = block.Header.PrevBlock
	}
	branch := make([]*wire.MsgBlock, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		branch = append(branch, reversed[i])
	}
	return branch, true
}

func (n *Node) pruneSideBranchLocked(branch []*wire.MsgBlock) {
	for _, block := range branch {
		hash := block.MustBlockHash()
		delete(n.sideBlocks, hash)
		prev := block.Header.PrevBlock
		n.sideByPrev[prev] = removeHash(n.sideByPrev[prev], hash)
		if len(n.sideByPrev[prev]) == 0 {
			delete(n.sideByPrev, prev)
		}
	}
}

func (n *Node) disconnectedBlocksForBranchLocked(branch []*wire.MsgBlock) []*wire.MsgBlock {
	if len(branch) == 0 {
		return nil
	}
	forkHash := branch[0].Header.PrevBlock
	fork, ok := n.cfg.Chain.BlockByHash(forkHash)
	if !ok {
		return nil
	}
	disconnected := make([]*wire.MsgBlock, 0)
	for height := n.cfg.Chain.Height(); height > fork.Header.Height; height-- {
		block, ok := n.cfg.Chain.BlockByHeight(height)
		if !ok {
			break
		}
		disconnected = append(disconnected, block)
	}
	return disconnected
}

func (n *Node) knowsSideOrMainPrevLocked(hash wire.Hash) bool {
	if _, ok := n.cfg.Chain.BlockByHash(hash); ok {
		return true
	}
	_, ok := n.sideBlocks[hash]
	return ok
}

func (n *Node) validateSideCandidateLocked(block *wire.MsgBlock) error {
	if len(block.Transactions) == 0 {
		return fmt.Errorf("side block has no transactions")
	}
	if block.Header.Height == 0 {
		return fmt.Errorf("side block cannot replace genesis")
	}
	if !chaincfg.MiningOpen(n.cfg.Params, time.Now().UTC()) {
		return fmt.Errorf("%s mining opens at %s", n.cfg.Params.Name, chaincfg.MiningStartTimeText(n.cfg.Params))
	}
	if block.Header.Height > n.cfg.Chain.Height()+defaultMaxOrphanHeightGap {
		return fmt.Errorf("side block height %d is too far ahead of tip %d", block.Header.Height, n.cfg.Chain.Height())
	}
	prev, ok := n.sideOrMainBlockLocked(block.Header.PrevBlock)
	if !ok {
		return fmt.Errorf("side block previous hash %s is unknown", block.Header.PrevBlock)
	}
	if block.Header.Height != prev.Header.Height+1 {
		return fmt.Errorf("side block height %d does not extend previous height %d", block.Header.Height, prev.Header.Height)
	}
	if err := consensus.CheckBlockTimestamp(n.cfg.Params, prev.Header.Timestamp, block.Header.Timestamp, time.Now().UTC()); err != nil {
		return fmt.Errorf("side block timestamp: %w", err)
	}
	expectedBits := consensus.CalcASERTNextBits(
		n.cfg.Params.GenesisBlock.Header.Bits,
		n.cfg.Params.GenesisBlock.Header.Timestamp,
		prev.Header.Timestamp,
		int64(block.Header.Height),
		n.cfg.Params,
	)
	if block.Header.Bits != expectedBits {
		return fmt.Errorf("side block bits %08x do not match expected %08x", block.Header.Bits, expectedBits)
	}
	root, err := wire.CalcMerkleRoot(block.Transactions)
	if err != nil {
		return err
	}
	if block.Header.MerkleRoot != root {
		return fmt.Errorf("side block merkle root mismatch")
	}
	if err := consensus.CheckProofOfWork(&block.Header, n.cfg.Params); err != nil {
		return fmt.Errorf("side block proof of work: %w", err)
	}
	return nil
}

func (n *Node) sideOrMainBlockLocked(hash wire.Hash) (*wire.MsgBlock, bool) {
	if block, ok := n.cfg.Chain.BlockByHash(hash); ok {
		return block, true
	}
	block, ok := n.sideBlocks[hash]
	return block, ok
}

func (n *Node) validateOrphanCandidateLocked(block *wire.MsgBlock) error {
	if len(block.Transactions) == 0 {
		return fmt.Errorf("orphan block has no transactions")
	}
	if block.Header.Height <= n.cfg.Chain.Height() {
		return fmt.Errorf("orphan height %d is not ahead of tip %d", block.Header.Height, n.cfg.Chain.Height())
	}
	if block.Header.Height > n.cfg.Chain.Height()+defaultMaxOrphanHeightGap {
		return fmt.Errorf("orphan height %d is too far ahead of tip %d", block.Header.Height, n.cfg.Chain.Height())
	}
	if err := consensus.CheckBlockTimestamp(n.cfg.Params, n.cfg.Chain.Tip().Header.Timestamp, block.Header.Timestamp, time.Now().UTC()); err != nil {
		return fmt.Errorf("orphan timestamp: %w", err)
	}
	root, err := wire.CalcMerkleRoot(block.Transactions)
	if err != nil {
		return err
	}
	if block.Header.MerkleRoot != root {
		return fmt.Errorf("orphan merkle root mismatch")
	}
	if err := consensus.CheckProofOfWork(&block.Header, n.cfg.Params); err != nil {
		return fmt.Errorf("orphan proof of work: %w", err)
	}
	return nil
}

func (n *Node) connectOrphansLocked(parentHash wire.Hash) []*wire.MsgBlock {
	hashes := append([]wire.Hash(nil), n.orphansByPrev[parentHash]...)
	delete(n.orphansByPrev, parentHash)
	connected := make([]*wire.MsgBlock, 0, len(hashes))
	for _, orphanHash := range hashes {
		orphan, ok := n.orphans[orphanHash]
		if !ok {
			continue
		}
		delete(n.orphans, orphanHash)
		if err := n.cfg.Chain.ValidateBlock(orphan); err != nil {
			n.logf("p2p orphan connect failed %s: %v", orphanHash, err)
			continue
		}
		if n.cfg.Store != nil {
			if err := n.cfg.Store.Append(orphan); err != nil {
				n.logf("p2p orphan persist failed %s: %v", orphanHash, err)
				continue
			}
		}
		if err := n.cfg.Chain.AddBlock(orphan); err != nil {
			n.logf("p2p orphan connect failed %s: %v", orphanHash, err)
			continue
		}
		connected = append(connected, orphan)
		connected = append(connected, n.connectOrphansLocked(orphanHash)...)
	}
	return connected
}

func removeHash(hashes []wire.Hash, target wire.Hash) []wire.Hash {
	out := hashes[:0]
	for _, hash := range hashes {
		if hash != target {
			out = append(out, hash)
		}
	}
	return out
}

func (n *Node) addBanScore(addr string, delta int, reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	key := banKey(addr)
	n.banScores[key] += delta
	if n.banScores[key] >= banThreshold {
		for peerAddr, peer := range n.peers {
			if banKey(peerAddr) == key && peer.conn != nil {
				_ = peer.conn.Close()
			}
		}
		n.logf("p2p banned %s score=%d reason=%s", key, n.banScores[key], reason)
	}
}

func (n *Node) RelayBlock(block *wire.MsgBlock) {
	if block == nil {
		return
	}
	n.broadcastInventory(block.MustBlockHash(), "")
}

func (n *Node) RelayTransaction(tx *wire.MsgTx) {
	if tx == nil {
		return
	}
	n.broadcastInv([]InvVector{{Type: InvTypeTx, Hash: tx.MustTxHash()}}, "")
}

func (n *Node) logf(format string, args ...any) {
	if n.cfg.Logger == nil {
		return
	}
	n.cfg.Logger.Printf(format, args...)
}

func randomNonce() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func shouldBanReadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	return true
}
