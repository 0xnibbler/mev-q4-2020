package model

import (
	"math/big"
	"strconv"
)

type AMM int

const (
	AMMUniswapV2 AMM = iota
	AMMSushiswap
)

func (a AMM) String() string {
	switch a % 100 {
	case AMMUniswapV2:
		return "UNIV2"
	case AMMSushiswap:
		return "SUSHI"
	}

	return "<" + strconv.Itoa(int(a)) + ">"
}

func AMMStoParams(aa []AMM) []*big.Int {
	res := make([]*big.Int, len(aa))
	for i, v := range aa {
		res[i] = new(big.Int).SetInt64(int64(v))
	}
	return res
}
