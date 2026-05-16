package consensus

import (
	"math"

	"github.com/pinancoin/pacd/internal/chaincfg"
)

func CalcBlockSubsidy(height int64, params *chaincfg.Params) int64 {
	if height <= 0 {
		return 0
	}

	subsidy := params.BaseSubsidy
	reductions := height / params.ReductionInterval
	for i := int64(0); i < reductions && subsidy > 0; i++ {
		subsidy *= params.MulSubsidy
		subsidy /= params.DivSubsidy
	}
	return subsidy
}

func CalcBlockOneTimeSplit(height int64, params *chaincfg.Params) (miner int64, project int64) {
	subsidy := CalcBlockSubsidy(height, params)
	miner = subsidy * params.MinerRewardPercent / 100
	project = subsidy - miner
	return miner, project
}

func EstimateTotalSubsidy(params *chaincfg.Params) int64 {
	var total int64
	subsidy := params.BaseSubsidy
	blocksAtSubsidy := params.ReductionInterval - 1
	for subsidy > 0 {
		add := subsidy * blocksAtSubsidy
		if total > math.MaxInt64-add {
			return total
		}
		total += add
		subsidy *= params.MulSubsidy
		subsidy /= params.DivSubsidy
		blocksAtSubsidy = params.ReductionInterval
	}
	return total
}
