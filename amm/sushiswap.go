package amm

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sync"

	"github.com/0xnibbler/mev-q4-2020/contracts/sushiswap"
	"github.com/0xnibbler/mev-q4-2020/model"
	"github.com/0xnibbler/mev-q4-2020/tokens"
	"github.com/0xnibbler/mev-q4-2020/util"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	sushiswapFee      = 3 / 1000
	sushiswapPairFile = "sushiswap_pairs.json"
)

var (
	sushiswapFactoryAddress = common.HexToAddress("0xc0aee478e3658e2610c5f7a4a2e1777ce9e4f2ac")
	LoadSushiswap           = true
)

type Sushiswap struct {
	AMMCommon

	pairToAddr map[Pair]common.Address
	pairs      map[common.Address]*SushiswapPair
	pairsLock  sync.RWMutex

	log logrus.FieldLogger
}

func NewSushiswap(a AMMCommon) *Sushiswap {
	s := &Sushiswap{
		AMMCommon:  a,
		pairToAddr: make(map[Pair]common.Address),
		pairs:      make(map[common.Address]*SushiswapPair),
		log:        a.metrics.WithField("context", "Sushiswap"),
	}

	if LoadSushiswap {
		s.tryLoad()
		s.log.Println("sushi: loaded:", len(s.pairs))
	} else {
		s.log.Println("sushi: LoadSushiswap = false")
	}

	return s
}

type SushiswapPair struct {
	Address common.Address `json:"address"`

	Token0 *tokens.Token `json:"token0"`
	Token1 *tokens.Token `json:"token1"`

	reserve0 *big.Int
	reserve1 *big.Int

	blockTimeLast int
}

func (s *Sushiswap) ID() model.AMM {
	return model.AMMSushiswap
}

func (s *Sushiswap) AddPair(address common.Address, t0, t1 *tokens.Token) {
	s.pairsLock.Lock()
	s.pairToAddr[Pair{Token0: t0.Address, Token1: t1.Address}] = address
	s.pairToAddr[Pair{Token0: t1.Address, Token1: t0.Address}] = address
	s.pairs[address] = &SushiswapPair{
		Address:  address,
		Token0:   t0,
		Token1:   t1,
		reserve0: new(big.Int),
		reserve1: new(big.Int),
	}

	s.pairsLock.Unlock()
}

func (s *Sushiswap) AllPairAddrs() []common.Address {
	s.pairsLock.Lock()
	defer s.pairsLock.Unlock()
	var aa []common.Address
	for _, p := range s.pairs {
		aa = append(aa, p.Address)
	}

	return aa
}

func (s *Sushiswap) GetReserves(ctx context.Context, a common.Address) (*big.Int, *big.Int, int, error) {
	instance, err := sushiswap.NewUniswapV2Pair(a, s.client)
	if err != nil {
		return nil, nil, 0, errors.Wrap(err, "create instance: "+a.String())
	}

	res, err := instance.GetReserves(&bind.CallOpts{Context: ctx})
	if err != nil {
		return nil, nil, 0, errors.Wrap(err, "get reserves: "+a.String())
	}

	return res.Reserve0, res.Reserve1, int(res.BlockTimestampLast), nil
}

func (s *Sushiswap) SyncAllOld(ctx context.Context) ([]common.Address, error) {
	s.pairsLock.Lock()
	defer s.pairsLock.Unlock()

	errg, ctx := errgroup.WithContext(ctx)
	for _, p := range s.pairs {
		p := p
		errg.Go(func() error {
			if p.Address == model.ZeroAddress {
				s.log.Println(p.Address.String())
				s.log.Println(p.Token0)
				s.log.Println(p.Token1)
				return nil
			}

			r0, r1, b, err := s.GetReserves(ctx, p.Address)
			if err != nil {
				return err
			}

			p.reserve0, p.reserve1, p.blockTimeLast = r0, r1, b
			return nil
		})
	}

	if err := errg.Wait(); err != nil {
		return nil, err
	}

	m := map[common.Address]struct{}{}
	for _, t := range s.tokens.AllAddresses() {
		m[t] = struct{}{}
	}

	errored := map[common.Address]struct{}{}

	for _, p := range s.pairs {
		err := false
		if _, ok := m[p.Token0.Address]; !ok {
			s.log.Println("not updating p =", p.Address.String(), "t =", p.Token0.Address.String())
			errored[p.Token0.Address] = struct{}{}
			err = true
		}

		if _, ok := m[p.Token1.Address]; !ok {
			s.log.Println("not updating p =", p.Address.String(), "t =", p.Token1.Address.String())
			errored[p.Token1.Address] = struct{}{}
			err = true
		}

		if err {
			continue
		}

		if err := s.updatePrices(p.Address); err != nil {
			return util.AddressMapToSlice(errored), errors.Wrap(err, "updatePrices")
		}
	}

	return util.AddressMapToSlice(errored), nil
}

func (s *Sushiswap) Update(a common.Address, r0, r1 *big.Int, btime int) error {
	//s.log.Println("s.Update", a.String(), "\t", r0.String(), r1.String())

	s.pairsLock.Lock()
	defer s.pairsLock.Unlock()
	p, ok := s.pairs[a]
	if !ok {
		s.log.Println("len(s.pairs)", len(s.pairs))
		return errors.New("pair not found: " + a.String())
	}

	p.reserve0 = r0
	p.reserve1 = r1
	p.blockTimeLast = btime

	return errors.Wrap(s.updatePrices(a), "update: updatePrices")
}

func (s *Sushiswap) updatePrices(a common.Address) error {
	p, ok := s.pairs[a]
	if !ok {
		return errors.New("pair not found")
	}

	// (t0) in1 -> (t1) out1
	// (t1) in2 -> (t0)update out2
	for _, amt := range model.AllAmts {
		var in1, in2, out1, out2 *big.Int

		var rate1, rate2 float64
		if t0IsWETH := p.Token0.IsWETH(); t0IsWETH || p.Token1.IsWETH() {
			if t0IsWETH {
				in1 = amt.Int()
				out1 = s.amtOut(in1, a, p.Token0.Address)
				if out1 == nil {
					continue
				}
				s.tokens.SetAmt(p.Token1.Address, out1, amt, s.ID()) // amtIn
				in2 = s.tokens.Amt(p.Token1.Address, amt)
				out2 = s.amtOut(in2, a, p.Token1.Address)
			} else {
				in2 = amt.Int()
				out2 = s.amtOut(in2, a, p.Token1.Address)
				if out2 == nil {
					continue
				}
				s.tokens.SetAmt(p.Token0.Address, out2, amt, s.ID()) // amtIn
				in1 = s.tokens.Amt(p.Token0.Address, amt)
				out1 = s.amtOut(in1, a, p.Token0.Address)
			}

			//s.log.Println("["+p.Token0.Symbol+":"+p.Token1.Symbol+"]    in1,in2", in1, in2, "    out1,out2", out1, out2)
		} else {
			in1, in2 = s.tokens.Amt(p.Token0.Address, amt), s.tokens.Amt(p.Token1.Address, amt)

			if in1 == nil && in2 == nil || in1 != nil && in2 != nil && in1.Sign() == 0 && in2.Sign() == 0 {
				continue

				s.log.Printf("pair: %s t0: %s (%s): %t t1: %s (%s): %t\n", a.String(),
					p.Token0.Address.String(), p.Token0.Symbol, in1 == nil,
					p.Token1.Address.String(), p.Token1.Symbol, in2 == nil,
				)
			} else if in2 == nil || in2.Sign() == 0 {
				out1 = s.amtOut(in1, a, p.Token0.Address)
				if out1 == nil {
					continue
				}

				s.tokens.SetAmt(p.Token1.Address, out1, amt, s.ID())
				in2 = s.tokens.Amt(p.Token1.Address, amt)
				out2 = s.amtOut(in2, a, p.Token1.Address)
			} else if in1 == nil || in1.Sign() == 0 {
				out2 = s.amtOut(in2, a, p.Token1.Address)
				if out2 == nil {
					continue
				}

				s.tokens.SetAmt(p.Token0.Address, out2, amt, s.ID())
				in1 = s.tokens.Amt(p.Token0.Address, amt)
				out1 = s.amtOut(in1, a, p.Token0.Address)
			} else {
				out1 = s.amtOut(in1, a, p.Token0.Address)
				out2 = s.amtOut(in2, a, p.Token1.Address)

			}

		}

		if out1 == nil || out2 == nil {
			continue
			//return errors.New("out = <nil>")
		}
		if in1 == nil || in2 == nil {
			continue
			//return errors.New("in = <nil>")
		}

		if in1.Sign() == 0 || in2.Sign() == 0 {
			continue

			s.log.Println("univ2 in1 == 0 || in2 == 0",
				p.Address.String()+" "+p.Token0.Address.String()+" "+p.Token1.Address.String(),
				"in1", in1.String(), "in2", in2.String(), "out1", out1.String(), "out2", out2.String(), " , ",
				"reserve0", p.reserve0.String(), "reserve1", p.reserve1.String())
		} else {
			rate1, _ = new(big.Float).Quo(
				new(big.Float).SetInt(out1),
				new(big.Float).SetInt(in1),
			).Float64()
			rate2, _ = new(big.Float).Quo(
				new(big.Float).SetInt(out2),
				new(big.Float).SetInt(in2),
			).Float64()
		}

		s.prices.Update(p.Token0.Address, p.Token1.Address, s.ID(), rate1*math.Pow10(p.Token0.Decimals-p.Token1.Decimals), amt)
		s.prices.Update(p.Token1.Address, p.Token0.Address, s.ID(), rate2*math.Pow10(p.Token1.Decimals-p.Token0.Decimals), amt)

		//s.log.Println("call update prices", p.Token0.Symbol, p.Token1.Symbol)
		//s.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token0.Symbol, in1, out1)
		//s.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token1.Symbol, in2, out2)
	}
	if s.metrics != nil {
		s.metrics.MetricPoolUpdate(s.ID(), p.Address)
	}

	return nil
}

//func (s *Sushiswap) updatePrices(a common.Address) error {
//	p, ok := s.pairs[a]
//	if !ok {
//		return errors.New("pair not found")
//	}
//
//	for _, amt := range model.AllAmts {
//		in1, in2 := s.tokens.Amt(p.Token0.Address, amt), s.tokens.Amt(p.Token1.Address, amt)
//
//		if in1 == nil || in2 == nil {
//			s.log.Printf("pair: %s t0: %s (%s): %t t1: %s (%s): %t\n", a.String(),
//				p.Token0.Address.String(), p.Token0.Symbol, in1 == nil,
//				p.Token1.Address.String(), p.Token1.Symbol, in2 == nil,
//			)
//			return nil
//		}
//
//		out1 := s.amtOut(in1, a, p.Token0.Address)
//		out2 := s.amtOut(in2, a, p.Token1.Address)
//		if out1 == nil || out2 == nil {
//			return errors.New("out = <nil>")
//		}
//
//		var rate1, rate2 float64
//
//
//
//		if in1.Sign() == 0 || in2.Sign() == 0 {
//			rate1 = math.Inf(+1)
//			rate2 = math.Inf(+1)
//			s.log.Println("sushi in1 == 0 || in2 == 0",
//				p.Address.String()+" "+p.Token0.Address.String()+" "+p.Token1.Address.String(),
//				"in1", in1.String(), "in2", in2.String())
//
//			return nil
//		} else {
//			rate1, _ = new(big.Float).Quo(new(big.Float).SetInt(out1), new(big.Float).SetInt(in1)).Float64()
//			rate2, _ = new(big.Float).Quo(new(big.Float).SetInt(out2), new(big.Float).SetInt(in2)).Float64()
//		}
//
//		s.prices.Update(p.Token0.Address, p.Token1.Address, model.AMMSushiswap, rate1*math.Pow10(p.Token0.Decimals-p.Token1.Decimals), amt)
//		s.prices.Update(p.Token1.Address, p.Token0.Address, model.AMMSushiswap, rate2*math.Pow10(p.Token1.Decimals-p.Token0.Decimals), amt)
//	}
//
//	//s.log.Println("call update prices", p.Token0.Symbol, p.Token1.Symbol)
//	//s.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token0.Symbol, in1, out1)
//	//s.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token1.Symbol, in2, out2)
//
//	if s.metrics != nil {
//		s.metrics.MetricPoolUpdate(model.AMMSushiswap, p.Address)
//	}
//
//	return nil
//}

func (s *Sushiswap) amtOut(amtIn *big.Int, addr, tokenIn common.Address) *big.Int {
	p := s.pairs[addr]

	var reserveIn, reserveOut *big.Int
	if tokenIn == p.Token0.Address {
		reserveIn, reserveOut = p.reserve0, p.reserve1
	} else if tokenIn == p.Token1.Address {
		reserveIn, reserveOut = p.reserve1, p.reserve0
	} else {
		return nil
	}

	if new(big.Int).Sub(reserveIn, amtIn).Sign() == -1 {
		return new(big.Int)
	}

	num, denom := new(big.Int), new(big.Int)

	amtInWFee := new(big.Int).Mul(amtIn, big.NewInt(997))
	num.Mul(amtInWFee, reserveOut)
	resIn1000 := new(big.Int).Mul(reserveIn, big.NewInt(1000))
	denom.Add(amtInWFee, resIn1000)

	res := new(big.Int).Div(num, denom)

	if new(big.Int).Sub(reserveOut, res).Sign() == -1 {
		return new(big.Int)
	}

	return res
}

func (s *Sushiswap) GetPairAddress(ctx context.Context, t0, t1 common.Address) (common.Address, error) {
	instance, err := sushiswap.NewUniswapV2Factory(sushiswapFactoryAddress, s.client)
	if err != nil {
		return common.Address{}, err
	}

	return instance.GetPair(&bind.CallOpts{Context: ctx}, t0, t1)
}

func (s *Sushiswap) GetAllPools(ctx context.Context, from int) ([]*model.PoolsResp, error) {
	instance, err := sushiswap.NewUniswapV2Factory(sushiswapFactoryAddress, s.client)
	if err != nil {
		return nil, err
	}

	l, err := instance.AllPairsLength(&bind.CallOpts{Context: ctx})
	if err != nil {
		return nil, err
	}

	var pp []*model.PoolsResp

	pCh := make(chan *model.PoolsResp)

	stop := make(chan struct{})
	done := make(chan struct{})

	//defer close(stop)

	go func() {
		for {
			select {
			case <-stop:
				close(done)
				return
			case p := <-pCh:
				//s.log.Println("add", p)
				pp = append(pp, p)
			}
		}
	}()

	errg, ctx := errgroup.WithContext(ctx)

	pool := make(chan struct{}, 100)
	defer close(pool)

	for i := range make([]struct{}, int(l.Int64())-from) {
		i := i + from
		errg.Go(func() error {
			pool <- struct{}{}

			a, t0, t1, err := func() (a common.Address, t0, t1 common.Address, err error) {
				a, err = instance.AllPairs(&bind.CallOpts{Context: ctx}, new(big.Int).SetInt64(int64(i)))
				if err != nil {
					return
				}

				pairInstance, err := sushiswap.NewUniswapV2Pair(a, s.client)
				if err != nil {
					return
				}

				t0, err = pairInstance.Token0(&bind.CallOpts{Context: ctx})
				if err != nil {
					return
				}

				t1, err = pairInstance.Token1(&bind.CallOpts{Context: ctx})
				if err != nil {
					return
				}

				//t0, err = tokens.Get(ctx, s.client, token0)
				//if err != nil {
				//	return a, nil, nil, err
				//}
				//
				//t1, err = tokens.Get(ctx, s.client, token1)
				//if err != nil {
				//	return a, nil, nil, err
				//}

				return a, t0, t1, nil
			}()

			if err == nil {
				//pp = append(pp, &model.PoolsResp{A: a, T0: t0, T1: t1})
				pCh <- &model.PoolsResp{A: a, T0: t0, T1: t1}
				//s.log.Println("ch <-", a)
				//} else {
				//s.log.Println("fail add", a, err)
			}

			<-pool
			return nil
		})
	}

	if err := errg.Wait(); err != nil {
		return pp, err
	}

	close(stop)
	<-done

	return pp, nil
}

//
//func (s *Sushiswap) tryLoad() {
//	f, err := os.Open(sushiswapPairFile)
//	if err == nil {
//		defer f.Close()
//		if err = json.NewDecoder(f).Decode(&s.pairs); err != nil {
//			s.log.Println("read sushiswap pairs:", err)
//		} else {
//			for k, v := range s.pairs {
//				s.pairToAddr[Pair{Token0: v.Token0.Address, Token1: v.Token1.Address}] = k
//				s.pairToAddr[Pair{Token0: v.Token1.Address, Token1: v.Token0.Address}] = k
//			}
//		}
//	} else if os.IsNotExist(err) {
//		f, err = os.Create(sushiswapPairFile)
//		if err == nil {
//			f.Close()
//		}
//	}
//}
//func (s *Sushiswap) Save() error {
//	f, err := os.OpenFile(sushiswapPairFile, os.O_RDWR|os.O_CREATE, 0644)
//	if err != nil {
//		return err
//	}
//
//	defer f.Close()
//	s.pairsLock.Lock()
//	defer s.pairsLock.Unlock()
//	e := json.NewEncoder(f)
//	e.SetIndent("", "\t")
//
//	s.log.Println("Sushiswap save len =", len(s.pairs))
//
//	return e.Encode(s.pairs)
//}

func (s *Sushiswap) Save() error {
	s.pairsLock.Lock()
	s.log.Println("sushiswap saving len =", len(s.pairs))
	defer s.pairsLock.Unlock()

	return util.Save("sushiswap", s.pairs)
}

func (s *Sushiswap) tryLoad() {
	s.pairsLock.Lock()
	defer s.pairsLock.Unlock()

	err := util.Load("sushiswap", &s.pairs)
	if err != nil {
		fmt.Println("loading sushiswap", err)
	}

	for k, v := range s.pairs {
		s.pairToAddr[Pair{Token0: v.Token0.Address, Token1: v.Token1.Address}] = k
		s.pairToAddr[Pair{Token0: v.Token1.Address, Token1: v.Token0.Address}] = k
	}
}
