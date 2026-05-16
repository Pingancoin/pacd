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

	"github.com/Pingancoin/pacd/internal/chaincfg"
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
	n.readLoop(ctx, conn)
}

func (n *Node) handshake(conn net.Conn, inbound bool) (Version, error) {
	if err := conn.SetDeadline(time.Now().Add(defaultHandshakeTimeout)); err != nil {
		return Version{}, err
	}
	localVersion := NewVersion(n.cfg.Params.Name, n.cfg.Params.GenesisHash, n.cfg.BestHeight(), n.nonce, n.cfg.UserAgent)
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

func (n *Node) readLoop(ctx context.Context, conn net.Conn) {
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
		default:
			n.logf("p2p ignored command %q from %s", msg.Command, conn.RemoteAddr())
		}
		if ctx.Err() != nil {
			return
		}
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
