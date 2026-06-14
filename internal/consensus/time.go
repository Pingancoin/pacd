package consensus

import (
	"fmt"
	"time"

	"github.com/Pingancoin/pacd/internal/chaincfg"
)

const futureCatchUpStep = time.Second

func MaxAllowedBlockTime(params *chaincfg.Params, prevTimestamp int64, now time.Time) time.Time {
	prevTime := time.Unix(prevTimestamp, 0).UTC()
	if params.MaxFutureBlockTime <= 0 {
		return prevTime.Add(params.TargetTimePerBlock)
	}

	maxTime := now.UTC().Add(params.MaxFutureBlockTime)
	if prevTime.After(maxTime) {
		return prevTime.Add(futureCatchUpStep)
	}
	return maxTime
}

func NextBlockTime(params *chaincfg.Params, prevTimestamp int64, now time.Time) time.Time {
	prevTime := time.Unix(prevTimestamp, 0).UTC()
	nextTime := now.UTC()
	if !nextTime.After(prevTime) {
		nextTime = prevTime.Add(futureCatchUpStep)
	}
	maxTime := MaxAllowedBlockTime(params, prevTimestamp, now)
	if nextTime.After(maxTime) {
		return maxTime
	}
	return nextTime
}

func CheckBlockTimestamp(params *chaincfg.Params, prevTimestamp int64, blockTimestamp int64, now time.Time) error {
	prevTime := time.Unix(prevTimestamp, 0).UTC()
	blockTime := time.Unix(blockTimestamp, 0).UTC()
	if !blockTime.After(prevTime) {
		return fmt.Errorf("block timestamp must increase")
	}

	maxTime := MaxAllowedBlockTime(params, prevTimestamp, now)
	if blockTime.After(maxTime) {
		return fmt.Errorf("block timestamp %s is too far in the future; maximum allowed is %s", blockTime.Format(time.RFC3339), maxTime.Format(time.RFC3339))
	}
	return nil
}
