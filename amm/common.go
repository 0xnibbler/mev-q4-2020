package amm

import (
	"github.com/0xnibbler/mev-q4-2020/metrics"
	"github.com/0xnibbler/mev-q4-2020/model"
	"github.com/0xnibbler/mev-q4-2020/tokens"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type AMMCommon struct {
	rpc     *rpc.Client
	client  *ethclient.Client
	prices  PriceKeeper
	tokens  *tokens.List
	metrics *metrics.Metrics
}

func NewConfig(rpc *rpc.Client, prices PriceKeeper, tokens *tokens.List, metrics *metrics.Metrics) AMMCommon {
	return AMMCommon{
		rpc:  rpc,
		client:  ethclient.NewClient(rpc),
		prices:  prices,
		tokens:  tokens,
		metrics: metrics,
	}
}

type Pair struct {
	Token0 common.Address
	Token1 common.Address
}

type PriceKeeper interface {
	Update(fromToken, toToken common.Address, amm model.AMM, rate float64, a model.AMT)
}
