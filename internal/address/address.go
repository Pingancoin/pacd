package address

import (
	"crypto/sha256"
	"fmt"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"golang.org/x/crypto/ripemd160"
)

const hash160Size = 20

func Hash160(data []byte) []byte {
	sha := sha256.Sum256(data)
	hasher := ripemd160.New()
	_, _ = hasher.Write(sha[:])
	return hasher.Sum(nil)
}

func PubKeyHashAddress(params *chaincfg.Params, pubKeyHash []byte) (string, error) {
	if len(pubKeyHash) != hash160Size {
		return "", fmt.Errorf("pubkey hash length is %d, want %d", len(pubKeyHash), hash160Size)
	}
	return EncodeBase58Check(params.PubKeyHashAddrID, pubKeyHash), nil
}

func ScriptHashAddress(params *chaincfg.Params, scriptHash []byte) (string, error) {
	if len(scriptHash) != hash160Size {
		return "", fmt.Errorf("script hash length is %d, want %d", len(scriptHash), hash160Size)
	}
	return EncodeBase58Check(params.ScriptHashAddrID, scriptHash), nil
}

func AddressFromPubKey(params *chaincfg.Params, pubKey []byte) (string, []byte, []byte, error) {
	if err := validatePubKey(pubKey); err != nil {
		return "", nil, nil, err
	}
	pubKeyHash := Hash160(pubKey)
	addr, err := PubKeyHashAddress(params, pubKeyHash)
	if err != nil {
		return "", nil, nil, err
	}
	return addr, pubKeyHash, PayToPubKeyHashScript(pubKeyHash), nil
}

func AddressFromScript(params *chaincfg.Params, script []byte) (string, []byte, []byte, error) {
	scriptHash := Hash160(script)
	addr, err := ScriptHashAddress(params, scriptHash)
	if err != nil {
		return "", nil, nil, err
	}
	return addr, scriptHash, PayToScriptHashScript(scriptHash), nil
}

func PayToPubKeyHashScript(pubKeyHash []byte) []byte {
	return append([]byte{0x76, 0xa9, 0x14}, append(pubKeyHash, 0x88, 0xac)...)
}

func PayToScriptHashScript(scriptHash []byte) []byte {
	return append([]byte{0xa9, 0x14}, append(scriptHash, 0x87)...)
}

func MultiSigRedeemScript(required int, pubKeys [][]byte) ([]byte, error) {
	if required < 1 || required > 16 {
		return nil, fmt.Errorf("required signatures must be between 1 and 16")
	}
	if len(pubKeys) < required {
		return nil, fmt.Errorf("pubkey count %d is less than required signatures %d", len(pubKeys), required)
	}
	if len(pubKeys) > 16 {
		return nil, fmt.Errorf("pubkey count must be <= 16")
	}

	script := []byte{smallIntOpcode(required)}
	for i, pubKey := range pubKeys {
		if err := validatePubKey(pubKey); err != nil {
			return nil, fmt.Errorf("pubkey %d: %w", i+1, err)
		}
		script = appendPushData(script, pubKey)
	}
	script = append(script, smallIntOpcode(len(pubKeys)), 0xae)
	return script, nil
}

func validatePubKey(pubKey []byte) error {
	switch {
	case len(pubKey) == 33 && (pubKey[0] == 0x02 || pubKey[0] == 0x03):
		return nil
	case len(pubKey) == 65 && pubKey[0] == 0x04:
		return nil
	default:
		return fmt.Errorf("public key must be compressed secp256k1-like 33 bytes or uncompressed 65 bytes")
	}
}

func smallIntOpcode(v int) byte {
	if v == 0 {
		return 0x00
	}
	return byte(0x50 + v)
}

func appendPushData(script []byte, data []byte) []byte {
	if len(data) < 0x4c {
		script = append(script, byte(len(data)))
		return append(script, data...)
	}
	script = append(script, 0x4c, byte(len(data)))
	return append(script, data...)
}
