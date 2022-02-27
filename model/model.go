package model

import (
	"github.com/ethereum/go-ethereum/common"
)

var (
	ZeroAddress = common.HexToAddress("0x0000000000000000000000000000000000000000")

	WETHAddress = common.HexToAddress("0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2")
	USDCAddress = common.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48")
	USDTAddress = common.HexToAddress("0xdac17f958d2ee523a2206206994597c13d831ec7")
	WBTCAddress = common.HexToAddress("0x2260fac5e5542a773aa44fbcfedf7c193bc2c599")
	LINKAddress = common.HexToAddress("0x514910771AF9Ca656af840dff83E8264EcF986CA")

	UniswapV1FactoryAddress = common.HexToAddress("0xc0a47dFe034B400B47bDaD5FecDa2621de6c4d95")
	UniswapV2RouterAddress  = common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D")
)
