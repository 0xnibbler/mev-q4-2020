package tokens

import (
	"math/big"
	"sort"

	"github.com/0xnibbler/mev-q4-2020/model"
)

type amtKeeper struct {
	amts []*big.Int
	amms []model.AMM
}

var _ sort.Interface = &amtKeeper{}

func (a *amtKeeper) Update(amt *big.Int, amm model.AMM) {
	found := false
	for i, ae := range a.amms {
		if ae == amm {
			a.amts[i] = amt
			found = true
			break
		}
	}

	if !found {
		a.amts = append(a.amts, amt)
		a.amms = append(a.amms, amm)
	}

	sort.Sort(a)
}

func (a *amtKeeper) Len() int {
	return len(a.amms)
}

func (a *amtKeeper) Less(i int, j int) bool {
	return new(big.Int).Sub(a.amts[i], a.amts[j]).Sign() > 0
}

func (a *amtKeeper) Swap(i int, j int) {
	a.amts[i], a.amts[j], a.amms[i], a.amms[j] =
		a.amts[j], a.amts[i], a.amms[j], a.amms[i]
}
