package consensus

import (
	"math/big"
	"time"

	"github.com/pinancoin/pacd/internal/chaincfg"
)

const asertRadix = int64(1 << 16)

func CalcASERTNextBits(anchorBits uint32, anchorTime int64, prevTime int64, nextHeight int64, params *chaincfg.Params) uint32 {
	if nextHeight <= 1 {
		return anchorBits
	}

	anchorTarget := CompactToBig(anchorBits)
	target := calcASERTTarget(anchorTarget, anchorTime, prevTime, nextHeight, params)
	if target.Sign() <= 0 {
		return 1
	}
	if target.Cmp(params.PowLimit) > 0 {
		target = new(big.Int).Set(params.PowLimit)
	}
	return BigToCompact(target)
}

func calcASERTTarget(anchorTarget *big.Int, anchorTime int64, prevTime int64, nextHeight int64, params *chaincfg.Params) *big.Int {
	targetSpacing := int64(params.TargetTimePerBlock / time.Second)
	halfLife := int64(params.ASERTHalfLife / time.Second)
	if targetSpacing <= 0 || halfLife <= 0 {
		return new(big.Int).Set(anchorTarget)
	}

	timeDiff := prevTime - anchorTime
	heightDiff := nextHeight - 1
	idealTime := heightDiff * targetSpacing
	exponent := ((timeDiff - idealTime) * asertRadix) / halfLife

	shifts := exponent / asertRadix
	frac := exponent - shifts*asertRadix
	if frac < 0 {
		frac += asertRadix
		shifts--
	}

	factor := asertFactor(frac)
	next := new(big.Int).Mul(anchorTarget, big.NewInt(factor))
	next.Rsh(next, 16)

	if shifts < 0 {
		next.Rsh(next, uint(-shifts))
	} else {
		next.Lsh(next, uint(shifts))
	}
	return next
}

func asertFactor(frac int64) int64 {
	// Integer approximation of 2^x for x in [0, 1), scaled by 2^16.
	// Constants are the common ASERT fixed-point polynomial coefficients.
	x := big.NewInt(frac)
	x2 := new(big.Int).Mul(x, x)
	x3 := new(big.Int).Mul(x2, x)

	term1 := new(big.Int).Mul(big.NewInt(195_766_423_245_049), x)
	term2 := new(big.Int).Mul(big.NewInt(971_821_376), x2)
	term3 := new(big.Int).Mul(big.NewInt(5_127), x3)
	sum := new(big.Int).Add(term1, term2)
	sum.Add(sum, term3)
	sum.Add(sum, new(big.Int).Lsh(big.NewInt(1), 47))
	sum.Rsh(sum, 48)
	sum.Add(sum, big.NewInt(asertRadix))
	return sum.Int64()
}
