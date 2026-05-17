package p2p

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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
	defaultHandshakeTimeout  = 10 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultMaxPeers          = 32
	defaultMaxOrphans        = 128
	defaultDiscoveryInterval = 500 * time.Millisecond
	defaultDiscoveryRetry    = 5 * time.Second
	banThreshold             = 100
	banScoreMalformed        = 100
	banScoreInvalidChain     = 50
)

type Config struct {
	Params           *chaincfg.Params
	ListenAddr       string
	Connect          []string
	MaxPeers         int
	BestHeight       func() uint32
	Chain            *blockchain.Chain
	Store            *blockstore.Store
	ChainMu          *sync.Mutex
	HasTx            func(wire.Hash) bool
	TxByHash         func(wire.Hash) (*wire.MsgTx, bool)
	AcceptTx         func(*wire.MsgTx) (bool, error)
	OnBlockConnected func(*wire.MsgBlock)
	UserAgent        string
	Logger           *log.Logger
}

type Node struct {
	cfg      Config
	nonce    uint64
	listener net.Listener

	mu            sync.Mutex
	peers         map[string]*peerState
	knownAddrs    map[string]struct{}
	recentDials   map[string]time.Time
	orphans       map[wire.Hash]*wire.MsgBlock
	orphansByPrev map[wire.Hash][]wire.Hash
	banScores     map[string]int
}

type PeerInfo struct {
	Address     string
	Inbound     bool
	BestHeight  uint32
	UserAgent   string
	ConnectedAt time.Time
}

type peerState struct {
	info    PeerInfo
	conn    net.Conn
	writeMu sync.Mutex
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
		knownAddrs:    make(map[string]struct{}),
		recentDials:   make(map[string]time.Time),
		orphans:       make(map[wire.Hash]*wire.MsgBlock),
		orphansByPrev: make(map[wire.Hash][]wire.Hash),
		banScores:     make(map[string]int),
	}
	node.rememberAddr(cfg.ListenAddr)
	for _, addr := range cfg.Connect {
		node.rememberAddr(addr)
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
	return len(n.knownAddrs)
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
		n.rememberAddr(listener.Addr().String())
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
		if err := n.connectAndHandle(ctx, addr); err != nil {
			n.logf("p2p connect %s failed: %v", addr, err)
		}
	}()
	return nil
}

func (n *Node) connectAndHandle(ctx context.Context, addr string) error {
	dialer := &net.Dialer{Timeout: defaultHandshakeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	n.handleConn(ctx, conn, false)
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
		go n.handleConn(ctx, conn, true)
	}
}

func (n *Node) connectLoop(ctx context.Context, addr string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := n.connectAndHandle(ctx, addr); err != nil {
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

func (n *Node) handleConn(ctx context.Context, conn net.Conn, inbound bool) {
	defer conn.Close()
	addr := conn.RemoteAddr().String()
	if !n.reservePeer(addr) {
		n.logf("p2p rejecting %s: max peers reached", addr)
		return
	}
	defer n.removePeer(addr)

	remoteVersion, err := n.handshake(conn, inbound)
	if err != nil {
		n.logf("p2p handshake %s failed: %v", addr, err)
		return
	}
	n.updatePeer(addr, PeerInfo{
		Address:     addr,
		Inbound:     inbound,
		BestHeight:  remoteVersion.BestHeight,
		UserAgent:   remoteVersion.UserAgent,
		ConnectedAt: time.Now().UTC(),
	}, conn)
	if !inbound {
		n.rememberAddr(addr)
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
	localVersion := NewVersion(n.cfg.Params.Name, n.cfg.Params.GenesisHash, n.bestHeight(), n.nonce, n.cfg.UserAgent)
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
		n.rememberAddr(addr)
	}
	return nil
}

func (n *Node) handleBlock(addr string, payload []byte) error {
	block, err := wire.DeserializeBlock(payload)
	if err != nil {
		return err
	}
	connected, connectedBlocks, err := n.connectBlock(block)
	if err != nil {
		return err
	}
	if connected {
		n.logf("p2p connected block height=%d hash=%s", block.Header.Height, block.MustBlockHash())
		for _, connectedBlock := range connectedBlocks {
			if n.cfg.OnBlockConnected != nil {
				n.cfg.OnBlockConnected(connectedBlock)
			}
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
	prevHash := n.cfg.Chain.Tip().MustBlockHash()
	prevTime := n.cfg.Chain.Tip().Header.Timestamp
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
		if header.Timestamp <= prevTime {
			return nil, fmt.Errorf("header timestamp must increase at height %d", header.Height)
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

func (n *Node) connectBlock(block *wire.MsgBlock) (bool, []*wire.MsgBlock, error) {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return false, nil, nil
	}
	if block.Header.Height <= n.cfg.Chain.Height() {
		if existing, ok := n.cfg.Chain.BlockByHeight(block.Header.Height); ok && existing.MustBlockHash() == block.MustBlockHash() {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("stale or conflicting block at height %d", block.Header.Height)
	}
	tip := n.cfg.Chain.Tip()
	if block.Header.PrevBlock != tip.MustBlockHash() || block.Header.Height != n.cfg.Chain.Height()+1 {
		n.addOrphanLocked(block)
		return false, nil, nil
	}
	if err := n.cfg.Chain.AddBlock(block); err != nil {
		return false, nil, err
	}
	if n.cfg.Store != nil {
		if err := n.cfg.Store.Append(block); err != nil {
			return false, nil, err
		}
	}
	connected := []*wire.MsgBlock{block}
	connected = append(connected, n.connectOrphansLocked(block.MustBlockHash())...)
	return true, connected, nil
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

func (n *Node) reservePeer(addr string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.peers) >= n.cfg.MaxPeers {
		return false
	}
	if _, ok := n.peers[addr]; ok {
		return false
	}
	if n.banScores[addr] >= banThreshold {
		return false
	}
	n.peers[addr] = &peerState{info: PeerInfo{Address: addr}}
	return true
}

func (n *Node) updatePeer(addr string, info PeerInfo, conn net.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	peer, ok := n.peers[addr]
	if !ok {
		peer = &peerState{}
		n.peers[addr] = peer
	}
	peer.info = info
	peer.conn = conn
}

func (n *Node) removePeer(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.peers, addr)
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
	addrs := make([]string, 0, len(n.knownAddrs))
	for addr := range n.knownAddrs {
		addrs = append(addrs, addr)
	}
	return addrs
}

func (n *Node) rememberAddr(addr string) {
	addr = normalizeAddr(addr)
	if addr == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.knownAddrs[addr] = struct{}{}
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
			if err := n.connectAndHandle(ctx, addr); err != nil && ctx.Err() == nil {
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
	candidates := make([]string, 0, len(n.knownAddrs))
	for addr := range n.knownAddrs {
		if addr == "" || addr == self {
			continue
		}
		if _, ok := n.peers[addr]; ok {
			continue
		}
		if n.isStaticPeer(addr) {
			continue
		}
		if n.banScores[addr] >= banThreshold {
			continue
		}
		if last, ok := n.recentDials[addr]; ok && now.Sub(last) < defaultDiscoveryRetry {
			continue
		}
		n.recentDials[addr] = now
		candidates = append(candidates, addr)
	}
	return candidates
}

func (n *Node) isStaticPeer(addr string) bool {
	for _, peer := range n.cfg.Connect {
		if normalizeAddr(peer) == addr {
			return true
		}
	}
	return false
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
	return net.JoinHostPort(host, port)
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
		if err := n.cfg.Chain.AddBlock(orphan); err != nil {
			n.logf("p2p orphan connect failed %s: %v", orphanHash, err)
			continue
		}
		if n.cfg.Store != nil {
			if err := n.cfg.Store.Append(orphan); err != nil {
				n.logf("p2p orphan persist failed %s: %v", orphanHash, err)
				continue
			}
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
	n.banScores[addr] += delta
	if n.banScores[addr] >= banThreshold {
		if peer, ok := n.peers[addr]; ok && peer.conn != nil {
			_ = peer.conn.Close()
		}
		n.logf("p2p banned %s score=%d reason=%s", addr, n.banScores[addr], reason)
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
