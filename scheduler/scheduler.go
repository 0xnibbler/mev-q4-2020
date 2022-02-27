package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0xnibbler/mev-q4-2020/metrics"
	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/sirupsen/logrus"
)

const MIN_TX_WAIT_TIME = time.Second * 1

var (
	Live       = true
	lastLiveTx time.Time
)

type Scheduler struct {
	cycles map[uint64]*model.Cycle

	client  *rpc.Client
	checker *checker

	newCycleCh chan []*model.Cycle
	remCycleCh chan map[uint64]struct{}
	updCycleCh chan map[uint64]float64
	resCycleCh chan map[uint64]*model.RunResult

	xLive exec

	log     logrus.FieldLogger
	metrics *metrics.Metrics
}

type exec interface {
	Running() bool
	Run(ctx context.Context, c *model.Cycle) (*model.RunResult, error)
}

func New(client *rpc.Client /*, xt texec*/, xl exec, m *metrics.Metrics /*, gas *gas.Tracker*/) *Scheduler {
	ch, err := newChecker(client,
		common.HexToAddress("0xf94e0580684b30c18249b270262232a5fd145611"),
		common.HexToAddress("0xb8a68725d217e5cd7f7a13df51c2116ef3576917"),
	)

	if err != nil {
		panic(err)
	}

	return &Scheduler{
		client:  client,
		checker: ch,
		xLive:   xl,
		cycles:  make(map[uint64]*model.Cycle),

		newCycleCh: make(chan []*model.Cycle, 100),
		remCycleCh: make(chan map[uint64]struct{}, 100),
		updCycleCh: make(chan map[uint64]float64, 100),
		resCycleCh: make(chan map[uint64]*model.RunResult, 100),

		metrics: m,
		log:     m.WithField("context", "Scheduler"),
	}
}

func (s *Scheduler) Add(c []*model.Cycle)          { s.newCycleCh <- c }
func (s *Scheduler) Update(cc map[uint64]float64)  { s.updCycleCh <- cc }
func (s *Scheduler) Remove(cc map[uint64]struct{}) { s.remCycleCh <- cc }

type triedCycleKey struct {
	R float64
	H uint64
}

func (s *Scheduler) Start(ctx context.Context) error {
	badCycles := sync.Map{}
	triedCycles := sync.Map{}

	testPool := make(chan struct{}, 10)

	tick := time.NewTicker(5 * time.Millisecond)
	n := 0
	for {
		select {
		case <-ctx.Done():
			tick.Stop()
			return nil

		case <-tick.C:
			//start := time.Now()
			n++
			/*
				var maxReturnCycle *model.Cycle
				var maxReturn float64
				for _, c := range s.cycles {
					if r := (c.Return - 1) * c.Amt.Float();
						r > maxReturn &&
							c.TestRes == nil &&
							len(c.ParamAMMs) <= 10 &&
							c.Amt <= model.AMT2 &&
							!badCycles[c.Hash()] &&
							c.TestRes == nil {
						maxReturn = r
						maxReturnCycle = c
					}
				}

				if maxReturnCycle != nil { //&& maxReturnCycle.TestRes == nil{
					s.Test(maxReturnCycle)
				}
			*/

			func() {
				var cc []*model.Cycle
				for _, c := range s.cycles {
					cc = append(cc, c)
				}
				sort.Slice(cc, func(i, j int) bool {
					return cc[i].Return < cc[j].Return
				})

				go func() {
					for _, c := range cc {
						if c.TestRes == nil &&
							c.Return > model.AmtThreshs[c.Amt] &&
							c.Return < 1.5 &&
							//!badCycles[c.Hash()] &&
							c.Amt <= model.MaxLiveAMT {
							if _, bad := badCycles.Load(c.Hash()); bad {
								continue
							}

							select {
							case testPool <- struct{}{}:
								s.Test(c, func() { <-testPool })
							default:
							}
						}
					}
				}()
			}()

			if n%2000 == 0 {
				var ops = ""
				for _, c := range s.cycles {
					if c.TestRes != nil && c.TestRes.Success && c.TestRes.Return != 0 {
						r := (c.Return - 1) * c.Amt.Float()
						r = c.TestRes.Return

						ops += fmt.Sprintf("age=%.1fs amt=%s ret=%.5f gas=%d hash=%d len=%d \n",
							c.Age().Seconds(), c.Amt.String(), r, c.TestRes.GasUsed, c.Hash(), len(c.ParamAddrs)) // todo: add test info
					}
				}
				//if maxReturnCycle != nil {
				//	ops += "\n" + fmt.Sprintf("max return: %d", maxReturnCycle.Hash())
				//	if tr := maxReturnCycle.TestRes; tr != nil {
				//		ops += fmt.Sprintf("r=%v  s=%v  err=%v", tr.Return, tr.Success, tr.Error)
				//	}
				//}
				if len(ops) > 0 {
					s.log.Println("ARB OPS\n" + ops)
				}
			}

			func() {
				var maxReturnCycle *model.Cycle
				var maxReturn float64

				for _, c := range s.cycles {
					if c.TestRes != nil && c.TestRes.Success && c.TestRes.Return != 0 {
						if r := c.TestRes.Return; 1+r > model.AmtThreshs[c.Amt] &&
							r > maxReturn &&
							c.TestRes != nil &&
							c.TestRes.Success &&
							c.Amt <= model.MaxLiveAMT {

							if when, tried := triedCycles.Load(triedCycleKey{R: c.TestRes.Return, H: c.Hash()}); tried &&
								time.Now().Sub(when.(time.Time)) > 10*time.Second {
								continue
							}

							maxReturn = r
							maxReturnCycle = c
						}
					}
				}

				if !Live || s.xLive.Running() ||
					maxReturnCycle == nil ||
					(!lastLiveTx.IsZero() && time.Now().Sub(lastLiveTx) < MIN_TX_WAIT_TIME) {

					return
				}

				select {
				case <-maxReturnCycle.Context.Done():
					return
				default:
				}

				s.log.Println("LIVE TX: starting   hash =", maxReturnCycle.Hash(), maxReturnCycle.Amt.String(), "return =", maxReturn)
				res, err := s.xLive.Run(maxReturnCycle.Context, maxReturnCycle)
				if err != nil {
					s.log.WithError(err).Error("LIVE TX: failed   hash =", maxReturnCycle.Hash())
					return
				}

				triedCycles.LoadOrStore(triedCycleKey{R: maxReturn, H: maxReturnCycle.Hash()}, time.Now())

				lastLiveTx = time.Now()

				s.log.Printf("LIVE TX: SUCCESS  hash = %d success = %t\n", maxReturnCycle.Hash(), res.Success)
			}()

		case mc := <-s.resCycleCh:
			for c, r := range mc {
				if cy, ok := s.cycles[c]; ok {
					cy.TestRes = r
					if r.Error != nil && strings.Contains(r.Error.Error(), "execution reverted: ") {
						//badCycles[cy.Hash()] = true
						badCycles.Store(cy.Hash(), true)
					}
				}
			}

		case mc := <-s.remCycleCh:
			for c := range mc {
				delete(s.cycles, c)
			}

		case mc := <-s.updCycleCh:
			for c, r := range mc {
				if cy, ok := s.cycles[c]; ok {
					cy.Return = r
				}
			}

		case cc := <-s.newCycleCh:
			for _, c := range cc {
				s.cycles[c.Hash()] = c
			}
		}
	}
}

func (s *Scheduler) Test(cy *model.Cycle, cb func()) {
	c := cy

	s.resCycleCh <- map[uint64]*model.RunResult{c.Hash(): {Success: false}}

	go func() {
		ctx, cancel := context.WithTimeout(c.Context, 1*time.Second)
		defer cancel()

		start := time.Now()
		ret, err := s.checker.check(ctx, c.Amt.Int(), c.ParamAddrs, model.AMMStoParams(c.ParamAMMs), c.Hash())
		dur := time.Now().Sub(start)

		if err != nil {
			fmt.Printf("TESTCYCLE:ERROR c=[%d] r=[%.5f] a=[%s] err=[%s] len=[%d] dur=[%v] \n", c.Hash(), c.Return, c.Amt.String(), err, len(c.ParamAddrs), dur)
			fmt.Printf("%v\n%v\n", c.ParamAddrs, c.ParamAMMs)

			s.resCycleCh <- map[uint64]*model.RunResult{c.Hash(): {Error: err}}
			cb()
			return
		}

		fmt.Printf("TESTCYCLE:SUCCESS c=[%d] r=[%.5f] a=[%s] ret=[%.5f] len=[%d] dur=[%v] \n", c.Hash(), c.Return, c.Amt.String(), ret, len(c.ParamAddrs), dur)
		fmt.Printf("%v\n%v\n", c.ParamAddrs, c.ParamAMMs)

		s.resCycleCh <- map[uint64]*model.RunResult{c.Hash(): {
			Success: true,
			Return:  ret,
		}}
		cb()
	}()

}

//func (s *Scheduler) Test(c *model.Cycle) {
//	ctx, cancel := context.WithTimeout(c.Context, 10*time.Second)
//	defer cancel()
//
//	startExec := time.Now()
//	res, err := s.xTest.Run(ctx, c)
//	s.log.Println("RUN DONE", time.Now().Sub(startExec), res, err)
//	if err != nil {
//		s.resCycleCh <- map[uint64]*model.RunResult{c.Hash(): {Failed: true}}
//		s.log.Println("exec ERROR:" + err.Error())
//	} else { // didnt get to here
//		s.resCycleCh <- map[uint64]*model.RunResult{c.Hash(): res}
//		fmt.Println("exec SUCCESS", time.Now().Sub(startExec), c.Hash(), time.Now())
//	}
//}
