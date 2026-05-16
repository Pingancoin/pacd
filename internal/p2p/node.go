package p2p

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/wire"
)

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultIdleTimeout      = 2 * time.Minute
	defaultMaxPeers         = 32
)

type Config struct {
	Params     *chaincfg.Params
	ListenAddr string
	Connect    []string
	MaxPeers   int
	BestHeight func() uint32
	Chain      *blockchain.Chain
	Store      *blockstore.Store
	ChainMu    *sync.Mutex
	UserAgent  string
	Logger     *log.Logger
}

type Node struct {
	cfg      Config
	nonce    uint64
	listener net.Listener

	mu    sync.Mutex
	peers map[string]PeerInfo
}

type PeerInfo struct {
	Address     string
	Inbound     bool
	BestHeight  uint32
	UserAgent   string
	ConnectedAt time.Time
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
	return &Node{
		cfg:   cfg,
		nonce: nonce,
		peers: make(map[string]PeerInfo),
	}, nil
}

func (n *Node) ListenAddr() string {
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

func (n *Node) Peers() []PeerInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	peers := make([]PeerInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (n *Node) Start(ctx context.Context) error {
	if n.cfg.ListenAddr != "" {
		listener, err := net.Listen("tcp", n.cfg.ListenAddr)
		if err != nil {
			return err
		}
		n.listener = listener
		go n.acceptLoop(ctx, listener)
	}

	for _, addr := range n.cfg.Connect {
		addr := addr
		go n.connectLoop(ctx, addr)
	}

	<-ctx.Done()
	if n.listener != nil {
		_ = n.listener.Close()
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
	})
	n.logf("p2p connected %s inbound=%t height=%d agent=%s", addr, inbound, remoteVersion.BestHeight, remoteVersion.UserAgent)
	if remoteVersion.BestHeight > n.bestHeight() {
		if err := n.sendGetHeaders(conn); err != nil {
			n.logf("p2p getheaders %s failed: %v", addr, err)
			return
		}
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
			return
		}
		switch msg.Command {
		case CommandPing:
			_ = WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandPong, Payload: msg.Payload})
		case CommandPong:
		case CommandGetHeaders:
			if err := n.handleGetHeaders(conn, msg.Payload); err != nil {
				n.logf("p2p getheaders from %s failed: %v", addr, err)
				return
			}
		case CommandHeaders:
			if err := n.handleHeaders(conn, msg.Payload); err != nil {
				n.logf("p2p headers from %s failed: %v", addr, err)
				return
			}
		case CommandGetBlocks:
			if err := n.handleGetBlocks(conn, msg.Payload); err != nil {
				n.logf("p2p getblocks from %s failed: %v", addr, err)
				return
			}
		case CommandBlock:
			if err := n.handleBlock(conn, addr, msg.Payload); err != nil {
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

func (n *Node) handleGetHeaders(conn net.Conn, payload []byte) error {
	req, err := DeserializeGetHeaders(payload)
	if err != nil {
		return err
	}
	headers := n.headersAfter(req.StartHeight)
	serialized, err := (Headers{Headers: headers}).Serialize()
	if err != nil {
		return err
	}
	return WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandHeaders, Payload: serialized})
}

func (n *Node) handleHeaders(conn net.Conn, payload []byte) error {
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
	return WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandGetBlocks, Payload: serialized})
}

func (n *Node) handleGetBlocks(conn net.Conn, payload []byte) error {
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
		if err := WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandBlock, Payload: serialized}); err != nil {
			return err
		}
	}
	return nil
}

func (n *Node) handleBlock(conn net.Conn, addr string, payload []byte) error {
	block, err := wire.DeserializeBlock(payload)
	if err != nil {
		return err
	}
	connected, err := n.connectBlock(block)
	if err != nil {
		return err
	}
	if connected {
		n.logf("p2p connected block height=%d hash=%s", block.Header.Height, block.MustBlockHash())
	}
	if info, ok := n.peerInfo(addr); ok && info.BestHeight > n.bestHeight() {
		return n.sendGetHeaders(conn)
	}
	return nil
}

func (n *Node) sendGetHeaders(conn net.Conn) error {
	payload, err := (GetHeaders{StartHeight: n.bestHeight()}).Serialize()
	if err != nil {
		return err
	}
	return WriteMessage(conn, n.cfg.Params.NetworkMagic, Message{Command: CommandGetHeaders, Payload: payload})
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

func (n *Node) connectBlock(block *wire.MsgBlock) (bool, error) {
	n.lockChain()
	defer n.unlockChain()
	if n.cfg.Chain == nil {
		return false, nil
	}
	if block.Header.Height <= n.cfg.Chain.Height() {
		if existing, ok := n.cfg.Chain.BlockByHeight(block.Header.Height); ok && existing.MustBlockHash() == block.MustBlockHash() {
			return false, nil
		}
		return false, fmt.Errorf("stale or conflicting block at height %d", block.Header.Height)
	}
	if err := n.cfg.Chain.AddBlock(block); err != nil {
		return false, err
	}
	if n.cfg.Store != nil {
		if err := n.cfg.Store.Append(block); err != nil {
			return false, err
		}
	}
	return true, nil
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
	n.peers[addr] = PeerInfo{Address: addr}
	return true
}

func (n *Node) updatePeer(addr string, info PeerInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[addr] = info
}

func (n *Node) removePeer(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.peers, addr)
}

func (n *Node) peerInfo(addr string) (PeerInfo, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	info, ok := n.peers[addr]
	return info, ok
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
