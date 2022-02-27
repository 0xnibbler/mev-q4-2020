package util

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xnibbler/mev-q4-2020/contracts/sushiswap"
	"github.com/0xnibbler/mev-q4-2020/contracts/uniswapv2"
	"github.com/0xnibbler/mev-q4-2020/model"
	"github.com/0xnibbler/mev-q4-2020/tokens"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
)

type router interface {
	GetAmountsOut(*bind.CallOpts, *big.Int, []common.Address) ([]*big.Int, error)
}

type poolPuller interface {
	GetAllPools(ctx context.Context, from int) ([]*model.PoolsResp, error)
	AddPair(a common.Address, t0, t1 *tokens.Token)
	Save() error
}

var uniV2RouterAddress = common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D")
var sushiRouterAddress = common.HexToAddress("0xd9e1cE17f2641f24aE83637ab66a2cca9C378B9F")

func PullAll(ctx context.Context, c *ethclient.Client, list *tokens.List, u2, su poolPuller) error {
	pp, err := u2.GetAllPools(ctx, 0)
	if err != nil {
		return errors.Wrap(err, "crv: GetAllPools")
	}

	routerU2, err := uniswapv2.NewUniswapV2Router02(uniV2RouterAddress, c)
	if err != nil {
		return err
	}

	tt0, tt1, err := getTokens(ctx, list, pp)

	testLiqAll(ctx, routerU2, pp, tt0, tt1, u2)

	if err := u2.Save(); err != nil {
		return err
	}

	pp, err = su.GetAllPools(ctx, 0)
	if err != nil {
		return errors.Wrap(err, "crv: GetAllPools")
	}

	routerSu, err := sushiswap.NewUniswapV2Router02(sushiRouterAddress, c)
	if err != nil {
		return err
	}

	tt0, tt1, err = getTokens(ctx, list, pp)

	testLiqAll(ctx, routerSu, pp, tt0, tt1, su)

	if err := su.Save(); err != nil {
		return err
	}

	if err := list.Save(); err != nil {
		return err
	}

	return nil
}

func getTokens(ctx context.Context, list *tokens.List, pools []*model.PoolsResp) (tt0, tt1 []*tokens.Token, err error) {
	tt0 = make([]*tokens.Token, len(pools))
	tt1 = make([]*tokens.Token, len(pools))
	var total, success int
outer:
	for i, p := range pools {
		total++
		for j, a := range []common.Address{p.T0, p.T1} {
			if list.ByAddr(a) == nil {
				err := list.Add(ctx, a)
				if err != nil {
					continue outer
				}
			}

			t := list.ByAddr(a)
			if t == nil {
				continue outer
			}

			if j == 0 {
				tt0[i] = t
			} else {
				tt1[i] = t
			}
		}
		success++
	}

	fmt.Println("tokens:", success, "/", total, "succeeded")

	return
}

func testLiqAll(ctx context.Context, router router, pp []*model.PoolsResp, tt0, tt1 []*tokens.Token, am poolPuller) {
	var success int
	for i, p := range pp {
		t0, t1 := tt0[i], tt1[i]

		if t0 == nil || t1 == nil {
			continue
		}

		f, err := testLiq(ctx, router, t0.Address, t1.Address)
		if err != nil || f < 0.9 {
			continue
		}

		success++
		am.AddPair(p.A, tt0[i], tt1[i])
	}

	fmt.Println("test liq", success, len(pp))

}

func testLiq(ctx context.Context, router router, from, to common.Address) (float64, error) {
	aa, err := router.GetAmountsOut(&bind.CallOpts{Context: ctx}, model.DefaultAMT.Int(), []common.Address{from, to})
	if err != nil {
		//fmt.Println("GetAmountsOut:", from.String(), to.String(), "\t", err)
		return 0, err
	}
	if aa[1].Int64() == 0 {
		//fmt.Println("GetAmountsOut:", from.String(), to.String(), "\t => 0")
		return 0, err
	}

	aaBack, err := router.GetAmountsOut(&bind.CallOpts{Context: ctx}, aa[1], []common.Address{to, from})
	if err != nil {
		//fmt.Println("GetAmountsOut (back):", from.String(), to.String(), "\t", err)
		return 0, err
	}

	if aaBack[1].Int64() == 0 {
		//fmt.Println("GetAmountsOut (back):", from.String(), to.String(), "\t => 0")
		return 0, err
	}

	f, _ := new(big.Float).Quo(
		new(big.Float).SetInt(aaBack[1]),
		new(big.Float).SetInt(model.DefaultAMT.Int())).Float64()

	return f, nil
}
