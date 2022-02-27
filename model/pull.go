package model

import (
	"github.com/ethereum/go-ethereum/common"
)

type PoolsResp struct {
	I  int
	A  common.Address
	T0 common.Address
	T1 common.Address
}
