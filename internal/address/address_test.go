package address_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/chaincfg"
)

func TestBase58CheckRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte{0x42}, 20)
	encoded := address.EncodeBase58Check(0x37, payload)
	version, decoded, err := address.DecodeBase58Check(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if version != 0x37 {
		t.Fatalf("version = %x", version)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestMainNetAddressesStartWithP(t *testing.T) {
	params := chaincfg.MainNetParams()
	payload := bytes.Repeat([]byte{0x01}, 20)

	pubKeyAddr, err := address.PubKeyHashAddress(params, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pubKeyAddr, "P") {
		t.Fatalf("pubkey hash address = %s", pubKeyAddr)
	}
	pubKeyScript, err := address.DecodeAddressScript(params, pubKeyAddr)
	if err != nil {
		t.Fatal(err)
	}
	if len(pubKeyScript) != 25 {
		t.Fatalf("p2pkh script length = %d", len(pubKeyScript))
	}

	scriptAddr, err := address.ScriptHashAddress(params, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(scriptAddr, "P") {
		t.Fatalf("script hash address = %s", scriptAddr)
	}
	scriptScript, err := address.DecodeAddressScript(params, scriptAddr)
	if err != nil {
		t.Fatal(err)
	}
	if len(scriptScript) != 23 {
		t.Fatalf("p2sh script length = %d", len(scriptScript))
	}
}

func TestMultiSigRedeemScript(t *testing.T) {
	keys := make([][]byte, 5)
	for i := range keys {
		keys[i] = append([]byte{0x02}, bytes.Repeat([]byte{byte(i + 1)}, 32)...)
	}
	script, err := address.MultiSigRedeemScript(3, keys)
	if err != nil {
		t.Fatal(err)
	}
	if script[0] != 0x53 {
		t.Fatalf("first opcode = %x, want OP_3", script[0])
	}
	if script[len(script)-2] != 0x55 || script[len(script)-1] != 0xae {
		t.Fatalf("tail = %s, want OP_5 OP_CHECKMULTISIG", hex.EncodeToString(script[len(script)-2:]))
	}
	addr, scriptHash, pkScript, err := address.AddressFromScript(chaincfg.MainNetParams(), script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "P") {
		t.Fatalf("multisig p2sh address = %s", addr)
	}
	if len(scriptHash) != 20 {
		t.Fatalf("script hash length = %d", len(scriptHash))
	}
	if len(pkScript) != 23 {
		t.Fatalf("p2sh script length = %d", len(pkScript))
	}
}
