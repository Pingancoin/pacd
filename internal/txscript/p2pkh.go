package txscript

import (
	"bytes"
	"fmt"

	"github.com/Pingancoin/pacd/internal/address"
	"github.com/Pingancoin/pacd/internal/wire"
	"github.com/decred/dcrd/crypto/blake256"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

const SigHashAll = byte(0x01)

func SignatureHash(tx *wire.MsgTx, inputIndex int, prevPkScript []byte) (wire.Hash, error) {
	if inputIndex < 0 || inputIndex >= len(tx.TxIn) {
		return wire.Hash{}, fmt.Errorf("input index %d out of range", inputIndex)
	}
	txCopy := &wire.MsgTx{
		Version:  tx.Version,
		TxIn:     make([]*wire.TxIn, len(tx.TxIn)),
		TxOut:    make([]*wire.TxOut, len(tx.TxOut)),
		LockTime: tx.LockTime,
	}
	for i, txIn := range tx.TxIn {
		sigScript := []byte(nil)
		if i == inputIndex {
			sigScript = append([]byte(nil), prevPkScript...)
		}
		txCopy.TxIn[i] = &wire.TxIn{
			PreviousOutPoint: txIn.PreviousOutPoint,
			SignatureScript:  sigScript,
			Sequence:         txIn.Sequence,
		}
	}
	for i, txOut := range tx.TxOut {
		txCopy.TxOut[i] = &wire.TxOut{
			Value:    txOut.Value,
			PkScript: append([]byte(nil), txOut.PkScript...),
		}
	}
	serialized, err := txCopy.Serialize()
	if err != nil {
		return wire.Hash{}, err
	}
	serialized = append(serialized, SigHashAll)
	return blake256.Sum256(serialized), nil
}

func SignP2PKHInput(tx *wire.MsgTx, inputIndex int, prevPkScript []byte, privKey *secp256k1.PrivateKey) error {
	hash, err := SignatureHash(tx, inputIndex, prevPkScript)
	if err != nil {
		return err
	}
	sig := ecdsa.Sign(privKey, hash[:]).Serialize()
	sig = append(sig, SigHashAll)
	pubKey := privKey.PubKey().SerializeCompressed()
	tx.TxIn[inputIndex].SignatureScript = pushData(pushData(nil, sig), pubKey)
	return nil
}

func VerifyP2PKHInput(tx *wire.MsgTx, inputIndex int, prevPkScript []byte) error {
	pubKeyHash, ok := ExtractPayToPubKeyHash(prevPkScript)
	if !ok {
		return fmt.Errorf("unsupported previous output script")
	}
	pushes, err := parsePushes(tx.TxIn[inputIndex].SignatureScript)
	if err != nil {
		return err
	}
	if len(pushes) != 2 {
		return fmt.Errorf("p2pkh signature script must push signature and public key")
	}
	sigBytes := pushes[0]
	if len(sigBytes) < 2 || sigBytes[len(sigBytes)-1] != SigHashAll {
		return fmt.Errorf("unsupported signature hash type")
	}
	pubKeyBytes := pushes[1]
	if !bytes.Equal(address.Hash160(pubKeyBytes), pubKeyHash) {
		return fmt.Errorf("public key does not match previous output")
	}
	pubKey, err := secp256k1.ParsePubKey(pubKeyBytes)
	if err != nil {
		return err
	}
	sig, err := ecdsa.ParseDERSignature(sigBytes[:len(sigBytes)-1])
	if err != nil {
		return err
	}
	hash, err := SignatureHash(tx, inputIndex, prevPkScript)
	if err != nil {
		return err
	}
	if !sig.Verify(hash[:], pubKey) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func ExtractPayToPubKeyHash(script []byte) ([]byte, bool) {
	if len(script) != 25 ||
		script[0] != 0x76 ||
		script[1] != 0xa9 ||
		script[2] != 0x14 ||
		script[23] != 0x88 ||
		script[24] != 0xac {
		return nil, false
	}
	return append([]byte(nil), script[3:23]...), true
}

func pushData(script []byte, data []byte) []byte {
	if len(data) >= 0x4c {
		script = append(script, 0x4c, byte(len(data)))
		return append(script, data...)
	}
	script = append(script, byte(len(data)))
	return append(script, data...)
}

func parsePushes(script []byte) ([][]byte, error) {
	var pushes [][]byte
	for len(script) > 0 {
		length := int(script[0])
		script = script[1:]
		if length == 0x4c {
			if len(script) == 0 {
				return nil, fmt.Errorf("truncated OP_PUSHDATA1")
			}
			length = int(script[0])
			script = script[1:]
		}
		if length > len(script) {
			return nil, fmt.Errorf("push length exceeds script length")
		}
		pushes = append(pushes, append([]byte(nil), script[:length]...))
		script = script[length:]
	}
	return pushes, nil
}
