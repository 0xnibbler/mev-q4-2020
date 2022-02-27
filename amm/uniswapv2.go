package amm

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sync"

	"github.com/0xnibbler/mev-q4-2020/contracts/uniswapv2"
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
	uniswapPairFile = "uniswap_pairs.json"
)

var (
	uniV2FactoryAddress = common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f")
	LoadUniswapV2       = true
)

// follow reserves (from this contract)
// + balances (from token contracts)
// see if can call skim()

type UniswapV2 struct {
	AMMCommon

	pairToAddr map[Pair]common.Address
	pairs      map[common.Address]*UniswapV2Pair
	pairsLock  sync.RWMutex

	log logrus.FieldLogger
}

func (u *UniswapV2) ID() model.AMM {
	return model.AMMUniswapV2
}

func NewUniswapV2(a AMMCommon) *UniswapV2 {
	u := &UniswapV2{
		AMMCommon:  a,
		pairToAddr: make(map[Pair]common.Address),
		pairs:      make(map[common.Address]*UniswapV2Pair),
		log:        a.metrics.WithField("context", "UniswapV2"),
	}

	if LoadUniswapV2 {
		u.tryLoad()
		u.log.Println("univ2: loaded:", len(u.pairs))
	} else {
		u.log.Println("univ2: LoadUniswapV2 = false")
	}

	return u
}

type UniswapV2Pair struct {
	Address common.Address `json:"address"`

	Token0 *tokens.Token `json:"token0"`
	Token1 *tokens.Token `json:"token1"`

	reserve0 *big.Int
	reserve1 *big.Int

	blockTimeLast int
}

func (u *UniswapV2) AddPair(address common.Address, t0, t1 *tokens.Token) {
	u.pairsLock.Lock()
	u.pairToAddr[Pair{Token0: t0.Address, Token1: t1.Address}] = address
	u.pairToAddr[Pair{Token0: t1.Address, Token1: t0.Address}] = address
	u.pairs[address] = &UniswapV2Pair{
		Address:       address,
		Token0:        t0,
		Token1:        t1,
		reserve0:      new(big.Int),
		reserve1:      new(big.Int),
		blockTimeLast: 0,
	}

	u.pairsLock.Unlock()
}

func (u *UniswapV2) AllPairAddrs() []common.Address {
	u.pairsLock.Lock()
	defer u.pairsLock.Unlock()
	var aa []common.Address
	for _, p := range u.pairs {
		aa = append(aa, p.Address)
	}

	return aa
}

func (u *UniswapV2) GetReserves(ctx context.Context, a common.Address) (*big.Int, *big.Int, int, error) {
	instance, err := uniswapv2.NewUniswapV2Pair(a, u.client)
	if err != nil {
		return nil, nil, 0, errors.Wrap(err, "create instance: "+a.String())
	}

	res, err := instance.GetReserves(&bind.CallOpts{Context: ctx})
	if err != nil {
		return nil, nil, 0, errors.Wrap(err, "get reserves: "+a.String())
	}

	return res.Reserve0, res.Reserve1, int(res.BlockTimestampLast), nil
}

func (u *UniswapV2) SyncAllOld(ctx context.Context) ([]common.Address, error) {
	u.pairsLock.Lock()
	defer u.pairsLock.Unlock()

	log := u.log

	errg, ctx := errgroup.WithContext(ctx)
	for _, p := range u.pairs {
		p := p
		errg.Go(func() error {
			if p.Address == model.ZeroAddress {
				log.Println(p.Address.String())
				log.Println(p.Token0)
				log.Println(p.Token1)
				return nil
			}

			r0, r1, b, err := u.GetReserves(ctx, p.Address)
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
	for _, t := range u.tokens.AllAddresses() {
		m[t] = struct{}{}
	}

	errored := map[common.Address]struct{}{}

	for _, p := range u.pairs {
		err := false
		if _, ok := m[p.Token0.Address]; !ok {
			log.Println("not updating p =", p.Address.String(), "t =", p.Token0.Address.String())
			errored[p.Token0.Address] = struct{}{}
			err = true
		}

		if _, ok := m[p.Token1.Address]; !ok {
			log.Println("not updating p =", p.Address.String(), "t =", p.Token1.Address.String())
			errored[p.Token1.Address] = struct{}{}
			err = true
		}

		if err {
			continue
		}

		if err := u.updatePrices(p.Address); err != nil {
			return util.AddressMapToSlice(errored), errors.Wrap(err, "updatePrices")
		}
	}

	return util.AddressMapToSlice(errored), nil
}

func (u *UniswapV2) Update(a common.Address, r0, r1 *big.Int, btime int) error {
	//u.log.Println("u.Update", a.String(), "\t", r0.String(), r1.String())

	u.pairsLock.Lock()
	defer u.pairsLock.Unlock()
	p, ok := u.pairs[a]
	if !ok {
		u.log.Println("len(u.pairs)", len(u.pairs))
		return errors.New("pair not found: " + a.String())
	}

	//fmt.Println("pool.UPDATE", u.ID(), ":", a.String())

	p.reserve0 = r0
	p.reserve1 = r1
	p.blockTimeLast = btime

	return errors.Wrap(u.updatePrices(a), "update: updatePrices")
}

func (u *UniswapV2) updatePrices(a common.Address) error {
	p, ok := u.pairs[a]
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
				out1 = u.amtOut(in1, a, p.Token0.Address)
				if out1 == nil {
					continue
				}
				u.tokens.SetAmt(p.Token1.Address, out1, amt, u.ID()) // amtIn
				in2 = u.tokens.Amt(p.Token1.Address, amt)
				out2 = u.amtOut(in2, a, p.Token1.Address)
			} else {
				in2 = amt.Int()
				out2 = u.amtOut(in2, a, p.Token1.Address)
				if out2 == nil {
					continue
				}
				u.tokens.SetAmt(p.Token0.Address, out2, amt, u.ID()) // amtIn
				in1 = u.tokens.Amt(p.Token0.Address, amt)
				out1 = u.amtOut(in1, a, p.Token0.Address)
			}

			//u.log.Println("["+p.Token0.Symbol+":"+p.Token1.Symbol+"]    in1,in2", in1, in2, "    out1,out2", out1, out2)
		} else {
			in1, in2 = u.tokens.Amt(p.Token0.Address, amt), u.tokens.Amt(p.Token1.Address, amt)

			if in1 == nil && in2 == nil || in1 != nil && in2 != nil && in1.Sign() == 0 && in2.Sign() == 0 {
				continue

				u.log.Printf("pair: %s t0: %s (%s): %t t1: %s (%s): %t\n", a.String(),
					p.Token0.Address.String(), p.Token0.Symbol, in1 == nil,
					p.Token1.Address.String(), p.Token1.Symbol, in2 == nil,
				)
			} else if in2 == nil || in2.Sign() == 0 {
				out1 = u.amtOut(in1, a, p.Token0.Address)
				if out1 == nil {
					continue
				}

				u.tokens.SetAmt(p.Token1.Address, out1, amt, u.ID())
				in2 = u.tokens.Amt(p.Token1.Address, amt)
				out2 = u.amtOut(in2, a, p.Token1.Address)
			} else if in1 == nil || in1.Sign() == 0 {
				out2 = u.amtOut(in2, a, p.Token1.Address)
				if out2 == nil {
					continue
				}

				u.tokens.SetAmt(p.Token0.Address, out2, amt, u.ID())
				in1 = u.tokens.Amt(p.Token0.Address, amt)
				out1 = u.amtOut(in1, a, p.Token0.Address)
			} else {
				out1 = u.amtOut(in1, a, p.Token0.Address)
				out2 = u.amtOut(in2, a, p.Token1.Address)

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

			u.log.Println("univ2 in1 == 0 || in2 == 0",
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

		u.prices.Update(p.Token0.Address, p.Token1.Address, u.ID(), rate1*math.Pow10(p.Token0.Decimals-p.Token1.Decimals), amt)
		u.prices.Update(p.Token1.Address, p.Token0.Address, u.ID(), rate2*math.Pow10(p.Token1.Decimals-p.Token0.Decimals), amt)

		//u.log.Println("call update prices", p.Token0.Symbol, p.Token1.Symbol)
		//u.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token0.Symbol, in1, out1)
		//u.log.Println(p.Token0.Symbol+":"+p.Token1.Symbol, p.Token1.Symbol, in2, out2)
	}
	if u.metrics != nil {
		u.metrics.MetricPoolUpdate(u.ID(), p.Address)
	}

	return nil
}

func (u *UniswapV2) amtOut(amtIn *big.Int, addr, tokenIn common.Address) (res *big.Int) {
	p := u.pairs[addr]

	var reserveIn, reserveOut *big.Int
	if tokenIn == p.Token0.Address {
		reserveIn, reserveOut = p.reserve0, p.reserve1
	} else if tokenIn == p.Token1.Address {
		reserveIn, reserveOut = p.reserve1, p.reserve0
	} else {
		return nil
	}

	if new(big.Int).Sub(reserveIn, amtIn).Sign() == -1 {
		return nil
	}

	num, denom := new(big.Int), new(big.Int)

	amtInWFee := new(big.Int).Mul(amtIn, big.NewInt(997))
	num.Mul(amtInWFee, reserveOut)
	resIn1000 := new(big.Int).Mul(reserveIn, big.NewInt(1000))
	denom.Add(amtInWFee, resIn1000)

	res = new(big.Int).Div(num, denom)

	if new(big.Int).Sub(reserveOut, res).Sign() == -1 {
		return nil
	}

	return res
}

func (u *UniswapV2) GetPairAddress(ctx context.Context, t0, t1 common.Address) (common.Address, error) {
	instance, err := uniswapv2.NewUniswapV2Factory(uniV2FactoryAddress, u.client)
	if err != nil {
		return common.Address{}, err
	}

	return instance.GetPair(&bind.CallOpts{Context: ctx}, t0, t1)
}

func (u *UniswapV2) GetAllPools(ctx context.Context, from int) ([]*model.PoolsResp, error) {
	instance, err := uniswapv2.NewUniswapV2Factory(uniV2FactoryAddress, u.client)
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
				//u.log.Println(p)
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

			a, t0, t1, err := func() (a, t0, t1 common.Address, err error) {
				a, err = instance.AllPairs(&bind.CallOpts{Context: ctx}, new(big.Int).SetInt64(int64(i)))
				if err != nil {
					return
				}

				pairInstance, err := uniswapv2.NewUniswapV2Pair(a, u.client)
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

				return a, t0, t1, nil
			}()

			if err == nil {
				pCh <- &model.PoolsResp{A: a, T0: t0, T1: t1, I: i}
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

//func (u *UniswapV2) tryLoad() {
//	f, err := os.Open(uniswapPairFile)
//	if err == nil {
//		defer f.Close()
//		if err = json.NewDecoder(f).Decode(&u.pairs); err != nil {
//			u.log.Println("read uniswap v2 pairs:", err)
//		} else {
//			for k, v := range u.pairs {
//				u.pairToAddr[Pair{Token0: v.Token0.Address, Token1: v.Token1.Address}] = k
//				u.pairToAddr[Pair{Token0: v.Token1.Address, Token1: v.Token0.Address}] = k
//			}
//		}
//	} else if os.IsNotExist(err) {
//		f, err = os.Create(uniswapPairFile)
//		if err == nil {
//			f.Close()
//		}
//	}
//}

//func (u *UniswapV2) Save() error {
//	f, err := os.OpenFile(uniswapPairFile, os.O_RDWR|os.O_CREATE, 0644)
//	if err != nil {
//		return err
//	}
//
//	defer f.Close()
//	u.pairsLock.Lock()
//	defer u.pairsLock.Unlock()
//	e := json.NewEncoder(f)
//	e.SetIndent("", "\t")
//
//	u.log.Println("UniswapV2 save len =", len(u.pairs))
//
//	return e.Encode(u.pairs)
//}

func (u *UniswapV2) Save() error {
	u.pairsLock.Lock()
	u.log.Println("uniswapv2 saving len =", len(u.pairs))
	defer u.pairsLock.Unlock()

	return util.Save("uniswapv2", u.pairs)
}

func (u *UniswapV2) tryLoad() {
	u.pairsLock.Lock()
	defer u.pairsLock.Unlock()

	err := util.Load("uniswapv2", &u.pairs)
	if err != nil {
		fmt.Println("loading uniswapv2", err)
	}

	for k, v := range u.pairs {
		u.pairToAddr[Pair{Token0: v.Token0.Address, Token1: v.Token1.Address}] = k
		u.pairToAddr[Pair{Token0: v.Token1.Address, Token1: v.Token0.Address}] = k
	}
}
