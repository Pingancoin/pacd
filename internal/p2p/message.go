package p2p

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/decred/dcrd/crypto/blake256"
)

const (
	headerSize     = 24
	commandSize    = 12
	MaxPayloadSize = 1 << 20

	CommandVersion    = "version"
	CommandVerAck     = "verack"
	CommandPing       = "ping"
	CommandPong       = "pong"
	CommandGetHeaders = "getheaders"
	CommandHeaders    = "headers"
	CommandGetBlocks  = "getblocks"
	CommandInv        = "inv"
	CommandGetData    = "getdata"
	CommandGetAddr    = "getaddr"
	CommandAddr       = "addr"
	CommandBlock      = "block"
)

type Message struct {
	Command string
	Payload []byte
}

func WriteMessage(w io.Writer, magic uint32, msg Message) error {
	if len(msg.Payload) > MaxPayloadSize {
		return fmt.Errorf("payload length %d exceeds max %d", len(msg.Payload), MaxPayloadSize)
	}
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:4], magic)
	copy(header[4:16], commandBytes(msg.Command))
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(msg.Payload)))
	copy(header[20:24], checksum(msg.Payload))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(msg.Payload) == 0 {
		return nil
	}
	_, err := w.Write(msg.Payload)
	return err
}

func ReadMessage(r io.Reader, magic uint32) (Message, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Message{}, err
	}
	gotMagic := binary.LittleEndian.Uint32(header[0:4])
	if gotMagic != magic {
		return Message{}, fmt.Errorf("network magic %08x does not match expected %08x", gotMagic, magic)
	}
	command := parseCommand(header[4:16])
	length := binary.LittleEndian.Uint32(header[16:20])
	if length > MaxPayloadSize {
		return Message{}, fmt.Errorf("payload length %d exceeds max %d", length, MaxPayloadSize)
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Message{}, err
		}
	}
	if !bytes.Equal(header[20:24], checksum(payload)) {
		return Message{}, fmt.Errorf("payload checksum mismatch")
	}
	return Message{Command: command, Payload: payload}, nil
}

func commandBytes(command string) []byte {
	command = strings.TrimSpace(command)
	b := make([]byte, commandSize)
	copy(b, []byte(command))
	return b
}

func parseCommand(b []byte) string {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		n = len(b)
	}
	return string(b[:n])
}

func checksum(payload []byte) []byte {
	hash := blake256.Sum256(payload)
	return hash[:4]
}
