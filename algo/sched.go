package algo

import (
	"github.com/0xnibbler/mev-q4-2020/model"
)

type sched interface {
	Add(c []*model.Cycle)
	Update(cc map[uint64]float64)
	Remove(cc map[uint64]struct{})
}

var _ sched = noopSched{}

type noopSched struct{}

func (s noopSched) Add([]*model.Cycle)         {}
func (s noopSched) Update(map[uint64]float64)  {}
func (s noopSched) Remove(map[uint64]struct{}) {}
