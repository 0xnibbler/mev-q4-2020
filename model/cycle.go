package model

import (
	"context"
	"math"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/hashstructure/v2"
)

type Half struct {
	To     int32
	Weight float64
	Amm    AMM
}

type Cycle struct {
	hash    uint64
	created time.Time

	Amt            AMT
	StartsWithWETH bool

	TestRes *RunResult

	Return   float64
	Path     []Half
	OrigPath []Half

	ParamAddrs []common.Address
	ParamAMMs  []AMM

	Context    context.Context
	CancelFunc context.CancelFunc
	OnCancel   func()
}

type RunResult struct {
	Success     bool
	Error       error
	GasUsed     uint64
	Return      float64
	MaxGasPrice uint64
}

type cycleHash struct {
	Amt        AMT
	ParamAddrs []common.Address
	ParamAMMs  []AMM
}

func NewCycle(p []Half, r float64, a AMT, w int32) *Cycle {
	c := &Cycle{
		created:  time.Now(),
		Return:   r,
		Path:     p,
		OrigPath: p,
		Amt:      a,
	}

	c.Context, c.CancelFunc = context.WithCancel(context.Background())

	c.sort()

	var minIdx = -1
	for j, p := range c.Path {
		if p.To == w {
			minIdx = j
		}
	}

	if minIdx != -1 {
		c.Path = append(c.Path[minIdx:], c.Path[:minIdx]...)
		c.StartsWithWETH = true
	}

	return c
}

func (c *Cycle) DoesntInclude(addrs ...common.Address) bool {
	for _, a := range c.ParamAddrs {
		for _, a2 := range addrs {
			if a == a2 {
				return false
			}
		}
	}

	return true
}

func (c *Cycle) SetParams(addrs []common.Address, amms []AMM) {
	c.ParamAddrs = addrs
	c.ParamAMMs = amms

	var err error
	c.hash, err = hashstructure.Hash(cycleHash{
		Amt:        c.Amt,
		ParamAddrs: c.ParamAddrs,
		ParamAMMs:  c.ParamAMMs,
	}, hashstructure.FormatV2, nil)
	if err != nil {
		panic(err)
	}
}

func (c *Cycle) Age() time.Duration {
	return time.Now().Sub(c.created)
}

func (c *Cycle) IsEquivalent(c2 *Cycle) bool {
	return c.hash == c2.hash
}

func (c *Cycle) Hash() uint64 {
	return c.hash
}

func (c *Cycle) sort() *Cycle {
	var min int32 = math.MaxInt32
	var minIdx = -1
	for j, p := range c.Path {
		if p.To < min {
			min = p.To
			minIdx = j
		}
	}

	c.Path = append(c.Path[minIdx:], c.Path[:minIdx]...)

	return c
}
