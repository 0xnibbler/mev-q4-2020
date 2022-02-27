package scheduler

import (
	"bytes"
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/pkg/errors"
)

var contractAbi = []byte("")

type checker struct {
	c *rpc.Client

	a    *abi.ABI
	from common.Address
	to   common.Address
}

func newChecker(c *rpc.Client, from, to common.Address) (*checker, error) {
	aFB, err := abi.JSON(bytes.NewReader(contractAbi))

	return &checker{c: c, a: &aFB, from: from, to: to}, err
}

func (ch *checker) check(ctx context.Context, amtIn *big.Int, tokens []common.Address, dexes []*big.Int, hash uint64) (float64, error) {

	data, err := ch.a.Pack("")
	if err != nil {
		return 0, errors.Wrap(err, "ch.a.Pack")
	}

	/*
		startP := time.Now()
		gasP, err := ch.estGas(ctx, ch.from, ch.to, nil, data, true)
		if err != nil {
			return 0, 0, errors.Wrap(err, fmt.Sprintf("ch.estGas  %d", hash))
		}
		endP := time.Now()
		gasL, err := ch.estGas(ctx, ch.from, ch.to, nil, data, false)
		if err != nil {
			return 0, 0, errors.Wrap(err, fmt.Sprintf("ch.estGas  %d", hash))
		}
		fmt.Println("CHECKER", "P", gasP, "L", gasL, "P==L", gasP == gasL, hash, endP.Sub(startP), time.Now().Sub(endP))
	*/

	return ch.call(ctx, ch.from, ch.to, nil, 1500000, data)
}

func (ch *checker) call(ctx context.Context, from, to common.Address, value *big.Int, gas uint64, data []byte) (latest float64, err error) {
	client := ethclient.NewClient(ch.c)

	msg := ethereum.CallMsg{
		From:       from,
		To:         &to,
		Gas:        gas,
		Value:      value,
		Data:       data,
		GasPrice:   nil,
		AccessList: nil,
	}

	c, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return 0, err
	}

	cRes, err := ch.a.Unpack("swap", c)
	if err != nil {
		return 0, err
	}

	return float64(cRes[0].(*big.Int).Int64()) / 1e+18, nil
}
