package algo

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/0xnibbler/mev-q4-2020/algo/graph"
	"github.com/0xnibbler/mev-q4-2020/metrics"
	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type Prices struct {
	amts map[model.AMT]*PricesAmt
}

type PricesAmt struct {
	amt model.AMT

	lock     sync.RWMutex
	vertices map[common.Address]int32
	graph    *graph.LabeledDirected

	scheduler sched

	cycles map[uint64]*model.Cycle

	updateCh chan updateMsg
	cycleCh  chan []*model.Cycle

	returnThresh float64
	metrics      *metrics.Metrics
	log          logrus.FieldLogger
}

type updateMsg struct {
	F, T common.Address
	E    model.AMM
	W    float64
}

func NewPrices(returnThreshs []float64, metrics *metrics.Metrics) *Prices {
	amts := map[model.AMT]*PricesAmt{}
	for i, a := range model.AllAmts {
		amts[a] = &PricesAmt{
			amt:      a,
			cycles:   make(map[uint64]*model.Cycle),
			vertices: map[common.Address]int32{model.WETHAddress: 0},
			graph: &graph.LabeledDirected{
				LabeledAdjacencyList: graph.LabeledAdjacencyList([][]model.Half{{}}),
			},
			updateCh:  make(chan updateMsg, 200),
			cycleCh:   make(chan []*model.Cycle, 100),
			scheduler: noopSched{},

			returnThresh: returnThreshs[i],
			metrics:      metrics,
			log:          metrics.WithField("context", "Prices"),
		}

	}

	return &Prices{
		amts: amts,
	}
}

func (p *Prices) SetScheduler(s sched) {
	for _, a := range p.amts {
		a.scheduler = s
	}
}

func (p *Prices) Start(ctx context.Context, interval time.Duration, newCh chan struct{}) error {
	eg, ctx := errgroup.WithContext(ctx)

	var news []chan struct{}
	if newCh != nil {
		news = make([]chan struct{}, len(p.amts))
		for i := range p.amts {
			news[i] = make(chan struct{})
		}
		go func() {
			for {
				select {
				case <-ctx.Done():
				case <-newCh:
					for i := range p.amts {
						go func(i model.AMT) { news[i] <- struct{}{} }(i)
					}

				}
			}
		}()
	}

	for i, amt := range p.amts {
		i, amt := i, amt
		var updateTick, newCyclesTick *time.Ticker
		if newCh != nil {
			updateTick = time.NewTicker(time.Hour * 8760)
			newCyclesTick = time.NewTicker(time.Hour * 8760)
		} else {
			updateTick = time.NewTicker(interval)
			newCyclesTick = time.NewTicker(interval)
		}
		eg.Go(func() error {
			defer updateTick.Stop()
			defer newCyclesTick.Stop()
			var newChIn chan struct{}
			if newCh != nil {
				newChIn = news[i]
			}
		outer:
			for {
				select {
				case <-ctx.Done():
					return nil

				case u := <-amt.updateCh:
					if u.W == 0 {
						continue
					}

					from, to := amt.getVertex(u.F), amt.getVertex(u.T)

					x := amt.graph.LabeledAdjacencyList[from]
					for i, y := range x {
						if y.To == to {
							if newW := -math.Log(u.W); x[i].Amm == u.E || newW < y.Weight && x[i].Amm != u.E {

								x[i].Weight = newW
								x[i].Amm = u.E
							}

							continue outer
						}
					}
					amt.graph.LabeledAdjacencyList[from] = append(x, model.Half{To: to, Weight: -math.Log(u.W), Amm: u.E})

				case cc := <-amt.cycleCh:
					if len(cc) == 0 {
						continue
					}

					added := amt.addCycle(cc...)

					if len(added) == 0 {
						continue
					}

					amt.scheduler.Add(added)

				case <-updateTick.C:
					amt.updateCycles()

				case <-newChIn:
					fmt.Println("case <-newChIn:")

					amt.updateCycles()

					gr, _ := amt.graph.Copy()

					weth := amt.vertices[model.WETHAddress]
					vv := amt.vertexLookup()

					go func() {
						select {
						case <-ctx.Done():
							return
						case <-time.After(interval):
							return
						default:
						}

						amt.negCycles(gr, weth, vv)
					}()

				case <-newCyclesTick.C:

					gr, _ := amt.graph.Copy()

					weth := amt.vertices[model.WETHAddress]
					vv := amt.vertexLookup()

					go func() {
						select {
						case <-ctx.Done():
							return
						case <-time.After(interval):
							return

						default:
						}

						amt.negCycles(gr, weth, vv)
					}()
				}
			}
		})
	}

	return eg.Wait()
}

func (p *Prices) Update(f, t common.Address, e model.AMM, w float64, a model.AMT) {
	p.amts[a].updateCh <- updateMsg{F: f, T: t, E: e, W: w}
}

func (p *PricesAmt) getVertex(t common.Address) int32 {
	if v, ok := p.vertices[t]; ok {
		return v
	}

	v := int32(len(p.vertices))
	p.vertices[t] = v
	p.graph.LabeledAdjacencyList = append(p.graph.LabeledAdjacencyList, []model.Half{})

	return v
}

func (p *PricesAmt) addCycle(cc ...*model.Cycle) []*model.Cycle {
	var added []*model.Cycle

	for _, c := range cc {
		if _, ok := p.cycles[c.Hash()]; !ok && c.Return >= p.returnThresh {
			added = append(added, c)
		}
	}

	for _, c := range added {
		p.cycles[c.Hash()] = c
	}

	return added
}

func (p *PricesAmt) vertexLookup() map[int32]common.Address {
	v := map[int32]common.Address{}
	for n, vv := range p.vertices {
		v[vv] = n
	}
	return v
}

func (p *PricesAmt) clearCycles() {
	if len(p.cycles) == 0 {
		return
	}
	remove := map[uint64]struct{}{}
	for h := range p.cycles {
		remove[h] = struct{}{}
		delete(p.cycles, h)
	}

	p.scheduler.Remove(remove)
}

func (p *PricesAmt) updateCycles() {
	if len(p.cycles) == 0 {
		return
	}

	dm := p.graph.DistanceMatrix()

	remove := map[uint64]struct{}{}
	update := map[uint64]float64{}
	oldReturns := map[uint64]float64{}

	if len(p.cycles) == 0 {
		return
	}

	for _, c1 := range p.cycles {
		w := 0.

		for i, pi := range c1.Path {
			j := (i + 1) % len(c1.Path)
			if pi == c1.Path[j] {
				continue
			}

			w += dm[pi.To][c1.Path[j].To]
		}

		if n := math.Exp(-w); n != c1.Return {
			oldReturns[c1.Hash()] = c1.Return
			c1.Return = n
			update[c1.Hash()] = n

			if c1.Return < p.returnThresh {
				remove[c1.Hash()] = struct{}{}
			}
		}

	}

	p.scheduler.Update(update)

	if len(remove) == 0 {
		return
	}

	for _, c := range p.cycles {
		if _, ok := remove[c.Hash()]; ok {
			c.CancelFunc()
			if c.OnCancel != nil {
				c.OnCancel()
			}

			delete(p.cycles, c.Hash())

			if p.metrics != nil {
				p.metrics.MetricCycle(len(c.Path), c.Hash(), 0, c.Amt)
				p.metrics.MetricCycleDur(len(c.Path), c.Hash(), oldReturns[c.Hash()], c.Age().Round(time.Millisecond*100).Seconds(), c.Amt)
			}
		}
	}

	p.scheduler.Remove(remove)
}

func (p *PricesAmt) negCycles(gr graph.LabeledDirected, weth int32, vv map[int32]common.Address) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	start := time.Now()
	gr.NegativeCycles(ctx, func(cc []model.Half) bool {
		if len(cc) == 2 && cc[0].Amm == cc[1].Amm {
			return true
		}

		d := .0
		var i []int32
		for _, h := range cc {
			d += h.Weight
			i = append(i, h.To)
		}

		if expRet := math.Exp(-d); !math.IsInf(d, +1) && expRet > 1 {
			c := model.NewCycle(cc, expRet, p.amt, weth)
			if c.StartsWithWETH {
				c.SetParams(path2Params(c.Path, vv))
				p.cycleCh <- []*model.Cycle{c}
			}
		}
		return true
	})

	fmt.Println("NEG CYCLES DONE", p.amt.String(), "STARTED", time.Now().Sub(start).Milliseconds(), "DONE", time.Now().Format(time.RFC3339Nano))
}

func path2Params(pt []model.Half, v map[int32]common.Address) (tokens []common.Address, exchanges []model.AMM) {
	for _, h := range pt {
		tokens = append(tokens, v[h.To])
		exchanges = append(exchanges, h.Amm)
	}

	return
}

type AmtSetter interface {
	SetAmt(t common.Address, amt *big.Int, amm model.AMM) (newAmt *big.Int)
}

func (p *Prices) GetDistToWETH(t common.Address) ([]float64, []model.AMM) {
	return nil, nil
}
