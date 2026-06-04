package stratum

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/blockstore"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/consensus"
	"github.com/Pingancoin/pacd/internal/mining"
	"github.com/Pingancoin/pacd/internal/wire"
)

const (
	defaultShareDifficulty = 4096
	defaultJobRefresh      = 30 * time.Second
	extraNonce1Size        = 4
	extraNonce2Size        = 8
	submitExtraDataSize    = extraNonce1Size + extraNonce2Size
	maxCachedJobs          = 256
)

type Options struct {
	ListenAddr      string
	MinerScript     []byte
	ShareDifficulty float64
	JobRefresh      time.Duration
	Logger          *log.Logger
}

type Server struct {
	chain           *blockchain.Chain
	store           *blockstore.Store
	mu              *sync.Mutex
	listenAddr      string
	minerScript     []byte
	shareDifficulty float64
	shareTarget     *big.Int
	jobRefresh      time.Duration
	logger          *log.Logger

	onBlockConnected func(*wire.MsgBlock)

	nextSession uint64
	nextJob     uint64

	sessionsMu sync.Mutex
	sessions   map[*session]struct{}

	jobsMu   sync.Mutex
	jobs     map[string]*job
	jobOrder []string
}

type job struct {
	id      string
	block   *wire.MsgBlock
	created time.Time
}

type session struct {
	server      *Server
	conn        net.Conn
	id          uint64
	extraNonce1 string

	mu           sync.Mutex
	remoteAddr   string
	connectedAt  time.Time
	lastSeenAt   time.Time
	subscribedAt time.Time
	authorizedAt time.Time
	lastJobAt    time.Time
	lastSubmitAt time.Time
	accepted     uint64
	rejected     uint64
	lastError    string

	user       string
	authorized bool
	sendMu     sync.Mutex
}

type Stats struct {
	Enabled         bool           `json:"enabled"`
	ListenAddr      string         `json:"listen_addr"`
	ShareDifficulty float64        `json:"share_difficulty"`
	JobRefreshSec   int64          `json:"job_refresh_sec"`
	ActiveJobs      int            `json:"active_jobs"`
	ConnectedMiners int            `json:"connected_miners"`
	ConnectedPeers  int            `json:"connected_peers"`
	Sessions        []SessionStats `json:"sessions"`
	Workers         []WorkerStats  `json:"workers"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type SessionStats struct {
	ID              uint64    `json:"id"`
	RemoteAddr      string    `json:"remote_addr"`
	Worker          string    `json:"worker"`
	Authorized      bool      `json:"authorized"`
	Online          bool      `json:"online"`
	Difficulty      float64   `json:"difficulty"`
	Accepted        uint64    `json:"accepted"`
	Rejected        uint64    `json:"rejected"`
	ConnectedAt     time.Time `json:"connected_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	SubscribedAt    time.Time `json:"subscribed_at,omitempty"`
	AuthorizedAt    time.Time `json:"authorized_at,omitempty"`
	LastJobAt       time.Time `json:"last_job_at,omitempty"`
	LastSubmitAt    time.Time `json:"last_submit_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	SessionExtra    string    `json:"session_extra"`
	ExtraNonce2Size int       `json:"extra_nonce2_size"`
}

type WorkerStats struct {
	Name           string    `json:"name"`
	Online         bool      `json:"online"`
	Difficulty     float64   `json:"difficulty"`
	Accepted       uint64    `json:"accepted"`
	Rejected       uint64    `json:"rejected"`
	Sessions       int       `json:"sessions"`
	RemoteAddrs    []string  `json:"remote_addrs"`
	ConnectedAt    time.Time `json:"connected_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	LastSubmitAt   time.Time `json:"last_submit_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastShareAt    time.Time `json:"last_share_at,omitempty"`
	LastAcceptedAt time.Time `json:"last_accepted_at,omitempty"`
}

type request struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
	Error  any             `json:"error"`
}

func New(chain *blockchain.Chain, store *blockstore.Store, mu *sync.Mutex, opts Options) (*Server, error) {
	if chain == nil {
		return nil, fmt.Errorf("chain is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if mu == nil {
		mu = &sync.Mutex{}
	}
	if len(opts.MinerScript) == 0 {
		return nil, fmt.Errorf("miner payout script is required")
	}
	listen := strings.TrimSpace(opts.ListenAddr)
	if listen == "" {
		listen = "127.0.0.1:9507"
	}
	diff := opts.ShareDifficulty
	if diff == 0 {
		diff = defaultShareDifficulty
	}
	target, err := difficultyTarget(diff, chain.Params())
	if err != nil {
		return nil, err
	}
	refresh := opts.JobRefresh
	if refresh == 0 {
		refresh = defaultJobRefresh
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		chain:           chain,
		store:           store,
		mu:              mu,
		listenAddr:      listen,
		minerScript:     append([]byte(nil), opts.MinerScript...),
		shareDifficulty: diff,
		shareTarget:     target,
		jobRefresh:      refresh,
		logger:          logger,
		sessions:        make(map[*session]struct{}),
		jobs:            make(map[string]*job),
	}, nil
}

func (s *Server) ListenAddr() string {
	return s.listenAddr
}

func (s *Server) ShareDifficulty() float64 {
	return s.shareDifficulty
}

func (s *Server) SetBlockConnectedCallback(fn func(*wire.MsgBlock)) {
	s.onBlockConnected = fn
}

func (s *Server) Stats() Stats {
	s.sessionsMu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for client := range s.sessions {
		sessions = append(sessions, client)
	}
	s.sessionsMu.Unlock()

	s.jobsMu.Lock()
	activeJobs := len(s.jobs)
	s.jobsMu.Unlock()

	stats := Stats{
		Enabled:         true,
		ListenAddr:      s.listenAddr,
		ShareDifficulty: s.shareDifficulty,
		JobRefreshSec:   int64(s.jobRefresh / time.Second),
		ActiveJobs:      activeJobs,
		ConnectedPeers:  len(sessions),
		UpdatedAt:       time.Now().UTC(),
	}
	workers := make(map[string]*WorkerStats)
	for _, client := range sessions {
		snapshot := client.snapshot(s.shareDifficulty)
		stats.Sessions = append(stats.Sessions, snapshot)
		if !snapshot.Authorized {
			continue
		}
		stats.ConnectedMiners++
		name := strings.TrimSpace(snapshot.Worker)
		if name == "" {
			name = snapshot.RemoteAddr
		}
		worker := workers[name]
		if worker == nil {
			worker = &WorkerStats{
				Name:        name,
				Online:      true,
				Difficulty:  snapshot.Difficulty,
				ConnectedAt: snapshot.ConnectedAt,
				LastSeenAt:  snapshot.LastSeenAt,
			}
			workers[name] = worker
		}
		worker.Sessions++
		worker.Accepted += snapshot.Accepted
		worker.Rejected += snapshot.Rejected
		worker.RemoteAddrs = append(worker.RemoteAddrs, snapshot.RemoteAddr)
		if snapshot.ConnectedAt.Before(worker.ConnectedAt) {
			worker.ConnectedAt = snapshot.ConnectedAt
		}
		if snapshot.LastSeenAt.After(worker.LastSeenAt) {
			worker.LastSeenAt = snapshot.LastSeenAt
		}
		if snapshot.LastSubmitAt.After(worker.LastSubmitAt) {
			worker.LastSubmitAt = snapshot.LastSubmitAt
			worker.LastShareAt = snapshot.LastSubmitAt
		}
		if snapshot.Accepted > 0 && snapshot.LastSubmitAt.After(worker.LastAcceptedAt) {
			worker.LastAcceptedAt = snapshot.LastSubmitAt
		}
		if snapshot.LastError != "" {
			worker.LastError = snapshot.LastError
		}
	}
	for _, worker := range workers {
		stats.Workers = append(stats.Workers, *worker)
	}
	sort.Slice(stats.Sessions, func(i, j int) bool {
		return stats.Sessions[i].ID < stats.Sessions[j].ID
	})
	sort.Slice(stats.Workers, func(i, j int) bool {
		return stats.Workers[i].Name < stats.Workers[j].Name
	})
	return stats
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go s.refreshLoop(ctx)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(s.jobRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.broadcastJob(false)
		}
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	now := time.Now().UTC()
	client := &session{
		server:      s,
		conn:        conn,
		id:          atomic.AddUint64(&s.nextSession, 1),
		extraNonce1: randomHex(extraNonce1Size),
		remoteAddr:  conn.RemoteAddr().String(),
		connectedAt: now,
		lastSeenAt:  now,
	}
	s.addSession(client)
	defer func() {
		s.removeSession(client)
		_ = conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		client.markSeen()
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			client.sendError(nil, 20, "invalid json")
			continue
		}
		client.handleRequest(req)
	}
}

func (c *session) markSeen() {
	c.mu.Lock()
	c.lastSeenAt = time.Now().UTC()
	c.mu.Unlock()
}

func (c *session) markSubscribed() {
	c.mu.Lock()
	c.subscribedAt = time.Now().UTC()
	c.mu.Unlock()
}

func (c *session) markAuthorized(user string) {
	c.mu.Lock()
	c.user = user
	c.authorized = true
	c.authorizedAt = time.Now().UTC()
	c.mu.Unlock()
}

func (c *session) markJobSent() {
	c.mu.Lock()
	c.lastJobAt = time.Now().UTC()
	c.mu.Unlock()
}

func (c *session) markSubmit(accepted bool, submitErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSubmitAt = time.Now().UTC()
	if submitErr != nil {
		c.rejected++
		c.lastError = submitErr.Error()
		return
	}
	if accepted {
		c.accepted++
		c.lastError = ""
	}
}

func (c *session) snapshot(diff float64) SessionStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return SessionStats{
		ID:              c.id,
		RemoteAddr:      c.remoteAddr,
		Worker:          c.user,
		Authorized:      c.authorized,
		Online:          true,
		Difficulty:      diff,
		Accepted:        c.accepted,
		Rejected:        c.rejected,
		ConnectedAt:     c.connectedAt,
		LastSeenAt:      c.lastSeenAt,
		SubscribedAt:    c.subscribedAt,
		AuthorizedAt:    c.authorizedAt,
		LastJobAt:       c.lastJobAt,
		LastSubmitAt:    c.lastSubmitAt,
		LastError:       c.lastError,
		SessionExtra:    c.extraNonce1,
		ExtraNonce2Size: extraNonce2Size,
	}
}

func (c *session) isAuthorized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authorized
}

func (c *session) workerName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.user
}

func (s *Server) addSession(client *session) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.sessions[client] = struct{}{}
}

func (s *Server) removeSession(client *session) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	delete(s.sessions, client)
}

func (c *session) handleRequest(req request) {
	switch req.Method {
	case "mining.subscribe":
		c.handleSubscribe(req)
	case "mining.authorize":
		c.handleAuthorize(req)
	case "mining.extranonce.subscribe":
		c.sendResult(req.ID, true)
	case "mining.submit":
		c.handleSubmit(req)
	case "client.get_version":
		c.sendResult(req.ID, "pacd-stratum/0.1")
	default:
		c.sendError(req.ID, 20, "unsupported method")
	}
}

func (c *session) handleSubscribe(req request) {
	result := []any{
		[][]string{
			{"mining.set_difficulty", "1"},
			{"mining.notify", "1"},
		},
		c.extraNonce1,
		extraNonce2Size,
	}
	c.sendResult(req.ID, result)
	c.send(map[string]any{
		"id":     nil,
		"method": "mining.set_difficulty",
		"params": []any{c.server.shareDifficulty},
	})
	c.markSubscribed()
}

func (c *session) handleAuthorize(req request) {
	params, err := stringParams(req.Params)
	if err != nil || len(params) < 1 {
		c.sendError(req.ID, 20, "invalid authorize params")
		return
	}
	c.markAuthorized(params[0])
	c.sendResult(req.ID, true)
	c.sendFreshJob(true)
}

func (c *session) handleSubmit(req request) {
	params, err := stringParams(req.Params)
	if err != nil || len(params) < 5 {
		c.sendError(req.ID, 20, "invalid submit params")
		return
	}
	if !c.isAuthorized() {
		c.sendError(req.ID, 24, "worker is not authorized")
		return
	}
	result, err := c.server.submit(c, params[1], params[2], params[3], params[4])
	if err != nil {
		c.markSubmit(false, err)
		c.sendError(req.ID, 21, err.Error())
		return
	}
	c.markSubmit(result, nil)
	c.sendResult(req.ID, result)
}

func (c *session) sendFreshJob(clean bool) {
	j, err := c.server.createJob()
	if err != nil {
		c.server.logger.Printf("stratum create job: %v", err)
		return
	}
	c.sendNotify(j, clean)
	c.markJobSent()
}

func (c *session) sendNotify(j *job, clean bool) {
	params, err := notifyParams(j, clean)
	if err != nil {
		c.server.logger.Printf("stratum notify: %v", err)
		return
	}
	c.send(map[string]any{
		"id":     nil,
		"method": "mining.notify",
		"params": params,
	})
}

func (c *session) sendResult(id json.RawMessage, result any) {
	c.send(response{
		ID:     normalizeID(id),
		Result: result,
		Error:  nil,
	})
}

func (c *session) sendError(id json.RawMessage, code int, message string) {
	c.send(response{
		ID:     normalizeID(id),
		Result: false,
		Error:  []any{code, message, nil},
	})
}

func (c *session) send(v any) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		c.server.logger.Printf("stratum marshal: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		c.server.logger.Printf("stratum write: %v", err)
	}
}

func (s *Server) createJob() (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	tipTime := time.Unix(s.chain.Tip().Header.Timestamp, 0).UTC()
	if !now.After(tipTime) {
		now = tipTime.Add(time.Second)
	}
	block, err := mining.NewCandidate(s.chain, s.minerScript, now)
	if err != nil {
		return nil, err
	}
	j := &job{
		id:      strconv.FormatUint(atomic.AddUint64(&s.nextJob, 1), 16),
		block:   block,
		created: now,
	}
	s.cacheJob(j)
	return j, nil
}

func (s *Server) cacheJob(j *job) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	s.jobs[j.id] = j
	s.jobOrder = append(s.jobOrder, j.id)
	for len(s.jobOrder) > maxCachedJobs {
		oldest := s.jobOrder[0]
		s.jobOrder = s.jobOrder[1:]
		delete(s.jobs, oldest)
	}
}

func (s *Server) lookupJob(id string) (*job, bool) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

func (s *Server) broadcastJob(clean bool) {
	j, err := s.createJob()
	if err != nil {
		s.logger.Printf("stratum broadcast job: %v", err)
		return
	}
	s.sessionsMu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for client := range s.sessions {
		if client.isAuthorized() {
			sessions = append(sessions, client)
		}
	}
	s.sessionsMu.Unlock()
	for _, client := range sessions {
		client.sendNotify(j, clean)
	}
}

func (s *Server) submit(client *session, jobID, extraNonceHex, ntimeHex, nonceHex string) (bool, error) {
	j, ok := s.lookupJob(jobID)
	if !ok {
		return false, fmt.Errorf("stale job")
	}
	block := cloneBlock(j.block)
	extraData, err := submitExtraData(client.extraNonce1, extraNonceHex)
	if err != nil {
		return false, err
	}
	block.Header.ExtraData = extraData
	ntime, err := strconv.ParseUint(ntimeHex, 16, 32)
	if err != nil {
		return false, fmt.Errorf("invalid ntime")
	}
	block.Header.Timestamp = int64(ntime)
	nonce, err := strconv.ParseUint(nonceHex, 16, 32)
	if err != nil {
		return false, fmt.Errorf("invalid nonce")
	}
	block.Header.Nonce = uint32(nonce)

	hash, err := block.BlockHash()
	if err != nil {
		return false, err
	}
	hashNum := consensus.HashToBig(hash)
	if hashNum.Cmp(s.shareTarget) > 0 {
		return false, fmt.Errorf("low difficulty share")
	}

	networkTarget := consensus.CompactToBig(block.Header.Bits)
	if hashNum.Cmp(networkTarget) > 0 {
		return true, nil
	}
	if err := s.acceptBlock(block); err != nil {
		return false, err
	}
	s.logger.Printf("stratum block accepted height=%d hash=%s worker=%s", block.Header.Height, hash, client.workerName())
	s.broadcastJob(true)
	return true, nil
}

func (s *Server) acceptBlock(block *wire.MsgBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.chain.ValidateBlock(block); err != nil {
		return err
	}
	if err := s.store.Append(block); err != nil {
		return err
	}
	if err := s.chain.AddBlock(block); err != nil {
		return err
	}
	if s.onBlockConnected != nil {
		s.onBlockConnected(block)
	}
	return nil
}

func notifyParams(j *job, clean bool) ([]any, error) {
	header, err := j.block.Header.Serialize()
	if err != nil {
		return nil, err
	}
	if len(header) != wire.MaxBlockHeaderPayload {
		return nil, fmt.Errorf("header length is %d, want %d", len(header), wire.MaxBlockHeaderPayload)
	}
	return []any{
		j.id,
		stratumPrevHash(header[4:36]),
		hex.EncodeToString(header[36:144]),
		hex.EncodeToString(header[176:180]),
		[]string{},
		hex.EncodeToString(header[0:4]),
		fmt.Sprintf("%08x", j.block.Header.Bits),
		fmt.Sprintf("%08x", uint32(j.block.Header.Timestamp)),
		clean,
	}, nil
}

func stratumPrevHash(prev []byte) string {
	out := make([]byte, len(prev))
	for i := 0; i < len(prev); i += 4 {
		remaining := len(prev) - i
		if remaining >= 4 {
			out[i] = prev[i+3]
			out[i+1] = prev[i+2]
			out[i+2] = prev[i+1]
			out[i+3] = prev[i]
			continue
		}
		copy(out[i:], prev[i:])
	}
	return hex.EncodeToString(out)
}

func submitExtraData(extraNonce1 string, submitted string) ([32]byte, error) {
	var extraData [32]byte
	submittedBytes, err := hex.DecodeString(strings.TrimSpace(submitted))
	if err != nil {
		return extraData, fmt.Errorf("invalid extranonce")
	}
	if len(submittedBytes) == extraNonce2Size && extraNonce1 != "" {
		prefix, err := hex.DecodeString(extraNonce1)
		if err != nil {
			return extraData, fmt.Errorf("invalid session extranonce")
		}
		submittedBytes = append(prefix, submittedBytes...)
	}
	if len(submittedBytes) == 0 || len(submittedBytes) > len(extraData) {
		return extraData, fmt.Errorf("invalid extranonce length")
	}
	copy(extraData[:], submittedBytes)
	return extraData, nil
}

func stringParams(raw []json.RawMessage) ([]string, error) {
	params := make([]string, 0, len(raw))
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err != nil {
			return nil, err
		}
		params = append(params, s)
	}
	return params, nil
}

func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func randomHex(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}

func difficultyTarget(diff float64, params *chaincfg.Params) (*big.Int, error) {
	if diff <= 0 || math.IsInf(diff, 0) || math.IsNaN(diff) {
		return nil, fmt.Errorf("invalid stratum difficulty %v", diff)
	}
	if diff < 1 {
		diff = 1
	}
	divisor := int64(math.Floor(diff))
	if divisor <= 0 {
		return nil, fmt.Errorf("invalid stratum difficulty %v", diff)
	}
	return new(big.Int).Div(new(big.Int).Set(params.PowLimit), big.NewInt(divisor)), nil
}

func cloneBlock(block *wire.MsgBlock) *wire.MsgBlock {
	clone := &wire.MsgBlock{
		Header:       block.Header,
		Transactions: make([]*wire.MsgTx, len(block.Transactions)),
	}
	copy(clone.Transactions, block.Transactions)
	return clone
}
