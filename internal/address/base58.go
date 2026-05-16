package address

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var bigRadix = big.NewInt(58)
var bigZero = big.NewInt(0)

func EncodeBase58Check(version byte, payload []byte) string {
	data := append([]byte{version}, payload...)
	data = append(data, checksum(data)...)
	return encodeBase58(data)
}

func DecodeBase58Check(encoded string) (byte, []byte, error) {
	decoded, err := decodeBase58(encoded)
	if err != nil {
		return 0, nil, err
	}
	if len(decoded) < 5 {
		return 0, nil, errors.New("base58check payload too short")
	}
	body := decoded[:len(decoded)-4]
	gotChecksum := decoded[len(decoded)-4:]
	if !bytes.Equal(gotChecksum, checksum(body)) {
		return 0, nil, errors.New("base58check checksum mismatch")
	}
	return body[0], append([]byte(nil), body[1:]...), nil
}

func checksum(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:4]
}

func encodeBase58(b []byte) string {
	x := new(big.Int).SetBytes(b)
	answer := make([]byte, 0, len(b)*136/100)
	for x.Cmp(bigZero) > 0 {
		mod := new(big.Int)
		x.DivMod(x, bigRadix, mod)
		answer = append(answer, alphabet[mod.Int64()])
	}
	for _, v := range b {
		if v != 0 {
			break
		}
		answer = append(answer, alphabet[0])
	}
	reverse(answer)
	return string(answer)
}

func decodeBase58(s string) ([]byte, error) {
	answer := big.NewInt(0)
	for _, r := range s {
		index := bytes.IndexRune([]byte(alphabet), r)
		if index < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", r)
		}
		answer.Mul(answer, bigRadix)
		answer.Add(answer, big.NewInt(int64(index)))
	}
	decoded := answer.Bytes()
	for _, r := range s {
		if byte(r) != alphabet[0] {
			break
		}
		decoded = append([]byte{0x00}, decoded...)
	}
	return decoded, nil
}

func reverse(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}
