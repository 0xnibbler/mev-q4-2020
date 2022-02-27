package graph

import (
	"context"
	"fmt"
	"math"

	"github.com/0xnibbler/mev-q4-2020/model"
)


func (g LabeledDirected) NegativeCycles(_ context.Context, emit func([]model.Half) bool) {
	prices := make(map[pricemapkey]*model.Half)

	for i, x := range g.LabeledAdjacencyList {
		for _, y := range x {
			if math.IsInf(y.Weight, 1) {
				panic(fmt.Sprintln(i, y.To, "inf"))
			}
			if y.Weight == 0 {
				panic(fmt.Sprintln(i, y.To, "0"))
			}

			prices[pricemapkey{int32(i), y.To}] = &y
		}
	}

	for _, y := range g.LabeledAdjacencyList[0] {
		if h, ok := prices[pricemapkey{from: y.To, to: 0}]; ok {
			if y.Weight+h.Weight < 0 {
				emit([]model.Half{y, {Weight: h.Weight, To: 0, Amm: h.Amm}})
			}
		}

		for _, z := range g.LabeledAdjacencyList[y.To] {
			if h, ok := prices[pricemapkey{from: z.To, to: 0}];
				ok && y.Weight+z.Weight+h.Weight < 0 {
				emit([]model.Half{y, z, {Weight: h.Weight, To: 0, Amm: h.Amm}})
			}
		}
	}


}

type pricemapkey struct {
	from, to int32
}

