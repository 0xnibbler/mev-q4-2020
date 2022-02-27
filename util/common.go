package util

import "github.com/ethereum/go-ethereum/common"

func AddressMapToSlice(m map[common.Address]struct{}) []common.Address {
	aa := []common.Address{}
	if len(m) == 0 {
		return aa
	}

	for k := range m {
		aa = append(aa, k)
	}

	return aa
}
