package main

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type UniswapV2PairsUpdater interface {
	SyncAllOld(ctx context.Context) ([]common.Address, error)
	SyncAll(ctx context.Context, limit int)
	Update(addr common.Address, r0, r1 *big.Int, btime int) error
	ID() model.AMM
}

func subsHeadPrices(ctx context.Context, client *ethclient.Client, hCh chan struct{}, u2, su UniswapV2PairsUpdater) error {
	headCh := make(chan *types.Header)
	headSubs, err := client.SubscribeNewHead(ctx, headCh)
	if err != nil {
		panic(err)
	}

	var last time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-headSubs.Err():
			if err.Error() == "client reconnected" {
				if headSubs, err = client.SubscribeNewHead(ctx, headCh); err != nil {
					return err
				}
				continue
			}

			return err
		case head := <-headCh:
			t := time.Now()

			fmt.Println("syncall new block\t\t\t", head.Number.String(), "\t", t.Sub(last).Milliseconds(), t.Format(time.RFC3339Nano))
			last = t

			start := time.Now()
			u2.SyncAll(ctx, 50)
			duru2 := time.Now().Sub(start)

			start = time.Now()
			su.SyncAll(ctx, 50)
			dursu := time.Now().Sub(start)

			fmt.Println("syncall", "durU2", duru2.Milliseconds(), "durSu", dursu.Milliseconds())

			hCh <- struct{}{}
		}
	}
}
