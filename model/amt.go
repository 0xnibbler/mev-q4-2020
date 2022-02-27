package model

import (
	"fmt"
	"math/big"
)

type AMT int

const (
	AMT0_5 AMT = iota
	AMT1
	AMT2
	AMT5
	AMT10

	AMTNUM
)

const (
	DefaultAMT = AMT1
	MaxLiveAMT = AMT10
)

func (a AMT) Float() float64 {
	switch a {
	case AMT0_5:
		return .5
	case AMT1:
		return 1
	case AMT2:
		return 2
	case AMT5:
		return 5
	case AMT10:
		return 10
	}
	return 0
}

var AllAmts []AMT
var AmtThreshs []float64


func init() {
	for i := 0; i < int(AMTNUM); i++ {
		AllAmts = append(AllAmts, AMT(i))
		AmtThreshs = append(AmtThreshs, 1+0.01/AMT(i).Float())
	}
}

func (a AMT) Int() *big.Int {
	return new(big.Int).Mul(
		new(big.Int).SetInt64(int64(a.Float()*1000)),
		new(big.Int).SetInt64(1e+15),
	)
}

func (a AMT) String() string {
	return fmt.Sprintf("Amt[%.1f]", a.Float())
}
