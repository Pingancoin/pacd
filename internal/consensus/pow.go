package consensus

import (
	"fmt"
	"math/big"

	"github.com/Pingancoin/pacd/internal/chaincfg"
	"github.com/Pingancoin/pacd/internal/wire"
)

func CompactToBig(compact uint32) *big.Int {
	mantissa := compact & 0x007fffff
	isNegative := compact&0x00800000 != 0
	exponent := uint(compact >> 24)

	var bn *big.Int
	if exponent <= 3 {
		mantissa >>= 8 * (3 - exponent)
		bn = big.NewInt(int64(mantissa))
	} else {
		bn = big.NewInt(int64(mantissa))
		bn.Lsh(bn, 8*(exponent-3))
	}
	if isNegative {
		bn.Neg(bn)
	}
	return bn
}

func BigToCompact(n *big.Int) uint32 {
	if n.Sign() == 0 {
		return 0
	}

	nAbs := new(big.Int).Set(n)
	if nAbs.Sign() < 0 {
		nAbs.Neg(nAbs)
	}

	exponent := uint(len(nAbs.Bytes()))
	var mantissa uint32
	if exponent <= 3 {
		mantissa = uint32(nAbs.Uint64() << (8 * (3 - exponent)))
	} else {
		tn := new(big.Int).Rsh(nAbs, 8*(exponent-3))
		mantissa = uint32(tn.Uint64())
	}

	if mantissa&0x00800000 != 0 {
		mantissa >>= 8
		exponent++
	}

	compact := uint32(exponent<<24) | (mantissa & 0x007fffff)
	if n.Sign() < 0 {
		compact |= 0x00800000
	}
	return compact
}

func HashToBig(hash wire.Hash) *big.Int {
	return new(big.Int).SetBytes(hash[:])
}

func CheckProofOfWork(header *wire.BlockHeader, params *chaincfg.Params) error {
	target := CompactToBig(header.Bits)
	if target.Sign() <= 0 {
		return fmt.Errorf("target difficulty is not positive")
	}
	if target.Cmp(params.PowLimit) > 0 {
		return fmt.Errorf("target %s exceeds pow limit %s", target, params.PowLimit)
	}

	hash, err := header.BlockHash()
	if err != nil {
		return err
	}
	if HashToBig(hash).Cmp(target) > 0 {
		return fmt.Errorf("block hash %s exceeds target %064x", hash, target)
	}
	return nil
}
