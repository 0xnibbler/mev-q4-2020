package tokens

import (
	"context"
	"github.com/0xnibbler/mev-q4-2020/contracts/erc20"
	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
)

type Token struct {
	Address  common.Address `json:"address"`
	Name     string         `json:"name"`
	Symbol   string         `json:"symbol"`
	Decimals int            `json:"decimals"`
	ChainID  int            `json:"chain_id"`
}

func (t *Token) IsWETH() bool {
	return t.Address == model.WETHAddress
}

func Get(ctx context.Context, c *ethclient.Client, a common.Address) (*Token, error) {
	instance, err := erc20.NewTokenCaller(a, c)
	if err != nil {
		return nil, errors.Wrap(err, "tl: get: new instance:")
	}

	co := &bind.CallOpts{Context: ctx}

	name, err := instance.Name(co)
	if err != nil {
		return nil, errors.Wrap(err, "tl: get: name:")
	}

	symbol, err := instance.Symbol(co)
	if err != nil {
		return nil, errors.Wrap(err, "tl: get: symbol:")
	}

	decimals, err := instance.Decimals(co)
	if err != nil {
		return nil, errors.Wrap(err, "tl: get: decimals:")
	}

	t := &Token{
		Address:  a,
		Symbol:   symbol,
		Name:     name,
		Decimals: int(decimals),
		ChainID:  1,
	}

	return t, nil
}
