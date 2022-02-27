package amm

import (
	"context"
	"fmt"
	"math/big"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"golang.org/x/sync/errgroup"
)

func (s *Sushiswap) SyncAll(ctx context.Context, limit int) {
	s.pairsLock.Lock()
	defer s.pairsLock.Unlock()

	var allpairs []common.Address
	for _, p := range s.pairs {
		allpairs = append(allpairs, p.Address)
	}

	to := common.HexToAddress("0x5EF1009b9FCD4fec3094a5564047e190D72Bd511")

	errg, ctx := errgroup.WithContext(ctx)
	var all, updates int64

	for i := 0; i < len(allpairs); i += limit {
		pairs := allpairs[i:min(i+limit, len(allpairs))]

		errg.Go(func() error {
			pairs := pairs
			data, err := uniswapv2QueryABI.Pack("getReservesByPairs", pairs)
			if err != nil {
				panic(err)
			}

			var result interface{}
			err = s.rpc.CallContext(ctx, &result, "eth_call",
				map[string]interface{}{
					"to":   to.Hex(),
					"data": "0x" + common.Bytes2Hex(data),
				}, "latest",
			)
			if err != nil {
				return err
			}

			bb, err := hexutil.Decode(result.(string))
			if err != nil {
				return err
			}

			up, err := uniswapv2QueryABI.Unpack("getReservesByPairs", bb)
			res := up[0].([][3]*big.Int)

			for i, a := range pairs {
				p, ok := s.pairs[a]
				if !ok {
					s.log.Println("len(s.pairs)", len(s.pairs))
					continue
				}

				atomic.AddInt64(&all, 1)
				atomic.AddInt64(&updates, 1)

				p.reserve0 = res[i][0]
				p.reserve1 = res[i][1]

				if err := s.updatePrices(a); err != nil {
					fmt.Println("syncall update prices", a.String(), err)
				}
			}

			return nil
		})
	}

	if err := errg.Wait(); err != nil {
		fmt.Println("sync all sushi err", err)
	}
}
