package tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/0xnibbler/mev-q4-2020/contracts/uniswapv2"
	"github.com/0xnibbler/mev-q4-2020/contracts/weth"
	"github.com/0xnibbler/mev-q4-2020/metrics"
	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/sirupsen/logrus"
)

type tAmt struct {
	T common.Address
	A model.AMT
}

type List struct {
	tokensLock sync.RWMutex
	tokens     map[common.Address]*Token
	tokenAmts  map[tAmt]*amtKeeper

	AmtGetter

	metrics *metrics.Metrics
	log     logrus.FieldLogger

	c *ethclient.Client
}

const listFile = "tokens.json"

var Load = true

type AmtGetter interface {
	GetDistToWETH(t common.Address) ([]float64, []model.AMM)
}

type AmtGetterNoop struct {
}

func (AmtGetterNoop) GetDistToWETH(common.Address) ([]float64, []model.AMM) {
	return nil, nil
}

func NewList(c *ethclient.Client, m *metrics.Metrics) *List {
	l := &List{
		c:         c,
		tokens:    make(map[common.Address]*Token),
		metrics:   m,
		AmtGetter: AmtGetterNoop{},
		tokenAmts: make(map[tAmt]*amtKeeper),
		log:       m.WithField("context", "List"),
	}

	if Load {
		l.tryLoad()
		l.log.Println("tokenlist: loaded:", len(l.tokens))
	} else {
		l.log.Println("tokenlist: Load = false")
	}

	return l
}

func (l *List) Add(ctx context.Context, a common.Address) error {
	if l.ByAddr(a) != nil {
		return nil
	}

	t, err := Get(ctx, l.c, a)
	if err != nil {
		return err
	}

	l.tokensLock.Lock()
	if _, ok := l.tokens[a]; !ok {
		l.tokens[a] = t
	}
	l.tokensLock.Unlock()

	return nil
}

func (l *List) SetAmts(ctx context.Context) ([]common.Address, error) {
	l.tokensLock.Lock()

	tokens := make(map[common.Address]Token)
	for _, v := range l.tokens {
		tokens[v.Address] = *v
	}
	l.tokensLock.Unlock()

	start := time.Now()

	instance, err := uniswapv2.NewUniswapV2Router02(model.UniswapV2RouterAddress, l.c)
	if err != nil {
		return nil, err
	}

	s := make(chan struct{})
	done := make(chan struct{})

	m := make(map[tAmt]*amtKeeper)
	var failed []common.Address

	mCh := make(chan struct {
		common.Address
		*big.Int
		model.AMT
	})
	fCh := make(chan common.Address)

	go func() {
		for {
			select {
			case a := <-mCh:
				k := tAmt{T: a.Address, A: a.AMT}
				if m[k] == nil {
					m[k] = &amtKeeper{}
				}
				m[k].Update(a.Int, model.AMMUniswapV2)
			case f := <-fCh:
				failed = append(failed, f)
			case <-s:
				close(done)
				return
			}
		}
	}()

	pool := make(chan struct{}, 20)

	wg := sync.WaitGroup{}

	for _, amt := range model.AllAmts {
		for a, t := range tokens {
			wg.Add(1)
			a, t := a, t
			amt := amt
			go func() {
				_ = t

				pool <- struct{}{}

				defer func() {
					<-pool
					wg.Done()
				}()

				if a == model.WETHAddress {
					mCh <- struct {
						common.Address
						*big.Int
						model.AMT
					}{Address: a, Int: amt.Int(), AMT: amt}

					return
				}

				aa, err := instance.GetAmountsOut(&bind.CallOpts{Context: ctx}, amt.Int(), []common.Address{model.WETHAddress, a})
				if err != nil {
					fCh <- a
					return
				}

				if aa[1].Int64() == 0 {
					fCh <- a
					return
				}

				mCh <- struct {
					common.Address
					*big.Int
					model.AMT
				}{Address: a, Int: aa[1], AMT: amt}
			}()
		}
	}

	wg.Wait()
	close(s)
	<-done

	l.log.Println("SetAmts dur:", time.Now().Sub(start))

	l.tokensLock.Lock()
	l.tokenAmts = m
	l.tokensLock.Unlock()
	return failed, nil

}

func (l *List) Amt(t common.Address, a model.AMT) (wad *big.Int) {
	k := tAmt{T: t, A: a}
	l.tokensLock.RLock()
	if l.tokenAmts[k] != nil && len(l.tokenAmts[k].amts) > 0 {
		wad = l.tokenAmts[k].amts[0]
	} else if false {
		l.log.WithField("token", t.String()).Warnln("Amt not found")
		ff, aa := l.GetDistToWETH(t)
		fmt.Println(ff, aa)
		for i := range ff {
			if ff[i] > 0 {
				l.log.WithField("token", t.String()).Warnln(wad, aa[i].String())

				wad, _ := new(big.Float).Mul(new(big.Float).SetFloat64(ff[i]), new(big.Float).SetInt(a.Int())).Int(nil)

				l.SetAmt(t, wad, a, aa[i])
			}
		}
		if l.tokenAmts[k] != nil && len(l.tokenAmts[k].amts) > 0 {
			l.log.Warnln(t.String(), l.tokenAmts[k].amts[0])
		} else {
			l.log.Warnln(t.String(), "still no amt")
		}
	}
	l.tokensLock.RUnlock()
	return
}

func (l *List) SetAmt(t common.Address, amt *big.Int, a model.AMT, amm model.AMM) (newAmt *big.Int) {
	k := tAmt{T: t, A: a}

	l.tokensLock.Lock()
	if l.tokenAmts[k] == nil {
		l.tokenAmts[k] = &amtKeeper{
			amts: []*big.Int{amt},
			amms: []model.AMM{amm},
		}
	} else {
		l.tokenAmts[k].Update(amt, amm)
	}

	newAmt = l.tokenAmts[k].amts[0]
	l.tokensLock.Unlock()

	return
}

func (l *List) ByAddr(a common.Address) *Token {
	l.tokensLock.Lock()
	defer l.tokensLock.Unlock()

	return l.tokens[a]
}

type loadToken struct {
	Address  string `json:"address"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	ChainID  int    `json:"chain_id"`
}

func (l *List) tryLoad() {
	q := make(map[string]loadToken)

	f, err := os.Open(listFile)
	if err == nil {
		defer f.Close()
		if err = json.NewDecoder(f).Decode(&q); err != nil {
			l.log.Println("read tokens:", err)
		}
		for k, v := range q {
			l.tokens[common.HexToAddress(k)] = &Token{
				Address:  common.HexToAddress(k),
				Name:     v.Name,
				Symbol:   v.Symbol,
				Decimals: v.Decimals,
				ChainID:  v.ChainID,
			}
		}

	} else if os.IsNotExist(err) {
		f, err = os.Create(listFile)
		if err == nil {
			f.Close()
		}
	}
}

func (l *List) Save() error {
	f, err := os.OpenFile(listFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	defer f.Close()
	e := json.NewEncoder(f)
	e.SetIndent("", "\t")

	l.log.Println("token list saving len =", len(l.tokens))

	return e.Encode(l.tokens)
}

var transferSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

type TokenTransferSubsFn interface {
	Transfer(in bool, addr common.Address, amt *big.Int)
}

func (l *List) SubscribeTransfers(ctx context.Context, tokenAddrs []common.Address, watchAddr []common.Address, fn TokenTransferSubsFn) error {
	query := ethereum.FilterQuery{
		Addresses: tokenAddrs,
		Topics:    [][]common.Hash{{transferSig}},
	}

	watchMap := make(map[common.Address]struct{})
	for _, a := range watchAddr {
		watchMap[a] = struct{}{}
	}

	fil, err := weth.NewWETHFilterer(common.Address{}, nil)
	if err != nil {
		return err
	}

	logs := make(chan types.Log)
	defer close(logs)

	sub, err := l.c.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return err
	}

	for {
		select {
		case err := <-sub.Err():
			return err

		case <-ctx.Done():
			return nil

		case vLog := <-logs:
			switch vLog.Topics[0] {
			case transferSig:
				event, err := fil.ParseTransfer(vLog)
				if err != nil {
					return err
				}

				_, in := watchMap[event.Dst]
				_, out := watchMap[event.Src]
				if !in && !out {
					continue
				}

				addr := event.Dst
				if out {
					addr = event.Src
				}

				fn.Transfer(in, addr, event.Wad)
			default:
				continue
			}
		}
	}
}

func (l *List) AllAddresses() []common.Address {
	l.tokensLock.RLock()
	var aa []common.Address
	for _, t := range l.tokens {
		aa = append(aa, t.Address)
	}
	l.tokensLock.RUnlock()

	return aa
}

func (l *List) Remove(a common.Address) bool {
	l.tokensLock.Lock()
	ok := l.tokens[a] != nil
	delete(l.tokens, a)
	l.tokensLock.Unlock()
	return ok
}
