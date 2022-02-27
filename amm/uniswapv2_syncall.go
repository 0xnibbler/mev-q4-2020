package amm

import (
	"bytes"
	"context"
	"fmt"
	"golang.org/x/sync/errgroup"
	"math/big"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

const (
	uniswapv2QueryABIJSON = `[{"inputs": [{"internalType": "contract UniswapV2Factory", "name": "_uniswapFactory", "type": "address"},{"internalType": "uint256", "name": "_start", "type": "uint256"},{"internalType": "uint256", "name": "_stop", "type": "uint256"}], "name": "getPairsByIndexRange", "outputs": [{"internalType": "address[3][]", "name": "", "type": "address[3][]"}], "stateMutability": "view", "type": "function"},{"inputs": [{"internalType": "contract IUniswapV2Pair[]", "name": "_pairs", "type": "address[]"}], "name": "getReservesByPairs", "outputs": [{"internalType": "uint256[3][]", "name": "", "type": "uint256[3][]"}], "stateMutability": "view", "type": "function"}]`
)

var uniswapv2QueryABI abi.ABI

func init() {
	var err error
	uniswapv2QueryABI, err = abi.JSON(bytes.NewReader([]byte(uniswapv2QueryABIJSON)))
	if err != nil {
		panic(err)
	}
}

func (u *UniswapV2) SyncAll(ctx context.Context, limit int) {
	u.pairsLock.Lock()
	defer u.pairsLock.Unlock()

	var allpairs []common.Address
	for _, p := range u.pairs {
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
			if err := u.rpc.CallContext(ctx, &result, "eth_call",
				map[string]interface{}{
					"to":   to.Hex(),
					"data": "0x" + common.Bytes2Hex(data),
				}, "latest",
			); err != nil {
				return err
			}

			bb, err := hexutil.Decode(result.(string))
			if err != nil {
				return err
			}

			up, err := uniswapv2QueryABI.Unpack("getReservesByPairs", bb)
			if err != nil {
				return err
			}

			res := up[0].([][3]*big.Int)

			for i, a := range pairs {
				p, ok := u.pairs[a]
				if !ok {
					u.log.Println("len(u.pairs)", len(u.pairs))
					continue
				}

				atomic.AddInt64(&all, 1)
				atomic.AddInt64(&updates, 1)

				p.reserve0 = res[i][0]
				p.reserve1 = res[i][1]

				if err := u.updatePrices(a); err != nil {
					fmt.Println("syncall update prices", a.String(), err)
				}

			}

			return nil
		})
	}

	if err := errg.Wait(); err != nil {
		fmt.Println("syncall uni err", err)
	}
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}
