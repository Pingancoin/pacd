package blockstore

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Pingancoin/pacd/internal/blockchain"
	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wire"
)

const BlocksFileName = "blocks.jsonl"

type blockRecord struct {
	Height uint32 `json:"height"`
	Hash   string `json:"hash"`
	Block  string `json:"block"`
}

type Store struct {
	dir  string
	path string
	mu   sync.Mutex
}

type RepairReport struct {
	Repaired    bool
	BackupPath  string
	TruncatedAt int64
	Reason      string
}

func New(dir string) *Store {
	return &Store{
		dir:  dir,
		path: filepath.Join(dir, BlocksFileName),
	}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load(params *chaincfg.Params) (*blockchain.Chain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chain, _, err := s.load(params)
	return chain, err
}

func (s *Store) Repair(params *chaincfg.Params) (*blockchain.Chain, RepairReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chain, offset, err := s.load(params)
	if err == nil {
		return chain, RepairReport{}, nil
	}
	if os.IsNotExist(err) {
		return blockchain.New(params), RepairReport{}, nil
	}
	if offset < 0 {
		return nil, RepairReport{}, err
	}
	backupPath, backupErr := s.backupCorruptStore()
	if backupErr != nil {
		return nil, RepairReport{}, fmt.Errorf("%w; backup failed: %v", err, backupErr)
	}
	if truncateErr := os.Truncate(s.path, offset); truncateErr != nil {
		return nil, RepairReport{}, fmt.Errorf("%w; truncate failed after backup %s: %v", err, backupPath, truncateErr)
	}
	repaired, _, loadErr := s.load(params)
	if loadErr != nil {
		return nil, RepairReport{}, fmt.Errorf("repair wrote backup %s but reload failed: %w", backupPath, loadErr)
	}
	return repaired, RepairReport{
		Repaired:    true,
		BackupPath:  backupPath,
		TruncatedAt: offset,
		Reason:      err.Error(),
	}, nil
}

func (s *Store) load(params *chaincfg.Params) (*blockchain.Chain, int64, error) {
	chain := blockchain.New(params)
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return chain, 0, nil
	}
	if err != nil {
		return nil, -1, err
	}
	defer file.Close()

	reader := newLineReader(file)
	line := 0
	var offset int64
	for {
		recordOffset := offset
		data, err := reader.readLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, recordOffset, fmt.Errorf("%s line %d: %w", s.path, line+1, err)
		}
		offset += int64(len(data))
		if len(data) == 0 {
			continue
		}
		line++
		block, err := parseBlockRecord(trimLineEnd(data))
		if err != nil {
			return nil, recordOffset, fmt.Errorf("%s line %d: %w", s.path, line, err)
		}
		if err := chain.AddBlock(block); err != nil {
			return nil, recordOffset, fmt.Errorf("%s line %d: %w", s.path, line, err)
		}
	}
	return chain, offset, nil
}

func (s *Store) Append(block *wire.MsgBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	record, err := newBlockRecord(block)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (s *Store) backupCorruptStore() (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	backupPath := fmt.Sprintf("%s.corrupt.%s", s.path, time.Now().UTC().Format("20060102T150405Z"))
	source, err := os.Open(s.path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	dest, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer dest.Close()
	if _, err := io.Copy(dest, source); err != nil {
		return "", err
	}
	if err := dest.Sync(); err != nil {
		return "", err
	}
	return backupPath, nil
}

func newBlockRecord(block *wire.MsgBlock) (blockRecord, error) {
	serialized, err := block.Serialize()
	if err != nil {
		return blockRecord{}, err
	}
	return blockRecord{
		Height: block.Header.Height,
		Hash:   block.MustBlockHash().String(),
		Block:  hex.EncodeToString(serialized),
	}, nil
}

func parseBlockRecord(line []byte) (*wire.MsgBlock, error) {
	var record blockRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, err
	}
	serialized, err := hex.DecodeString(record.Block)
	if err != nil {
		return nil, err
	}
	block, err := wire.DeserializeBlock(serialized)
	if err != nil {
		return nil, err
	}
	if block.Header.Height != record.Height {
		return nil, fmt.Errorf("record height %d does not match block height %d", record.Height, block.Header.Height)
	}
	if got := block.MustBlockHash().String(); got != record.Hash {
		return nil, fmt.Errorf("record hash %s does not match block hash %s", record.Hash, got)
	}
	return block, nil
}

type lineReader struct {
	reader *bufio.Reader
}

func newLineReader(r io.Reader) lineReader {
	return lineReader{reader: bufio.NewReaderSize(r, 64*1024)}
}

func (r lineReader) readLine() ([]byte, error) {
	line, err := r.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return []byte(line), nil
		}
		return nil, err
	}
	return []byte(line), nil
}

func trimLineEnd(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}
