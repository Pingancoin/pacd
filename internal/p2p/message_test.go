package p2p_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Pingancoin/pacd/internal/p2p"
)

func TestMessageRoundTrip(t *testing.T) {
	buf := new(bytes.Buffer)
	payload := []byte("hello")
	if err := p2p.WriteMessage(buf, 0xfacec0f1, p2p.Message{Command: p2p.CommandPing, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	msg, err := p2p.ReadMessage(buf, 0xfacec0f1)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Command != p2p.CommandPing || !bytes.Equal(msg.Payload, payload) {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestMessageRejectsWrongMagic(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := p2p.WriteMessage(buf, 0xfacec0f1, p2p.Message{Command: p2p.CommandPing}); err != nil {
		t.Fatal(err)
	}
	if _, err := p2p.ReadMessage(buf, 0xfacec0a1); err == nil || !strings.Contains(err.Error(), "network magic") {
		t.Fatalf("expected network magic error, got %v", err)
	}
}
