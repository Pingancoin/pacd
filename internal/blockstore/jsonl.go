package blockstore

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	chain := blockchain.New(params)
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return chain, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		block, err := parseBlockRecord(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", s.path, line, err)
		}
		if err := chain.AddBlock(block); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", s.path, line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return chain, nil
}

func (s *Store) Append(block *wire.MsgBlock) error {
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
