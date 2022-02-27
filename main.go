package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/0xnibbler/mev-q4-2020/algo"
	"github.com/0xnibbler/mev-q4-2020/amm"
	"github.com/0xnibbler/mev-q4-2020/fb"
	"github.com/0xnibbler/mev-q4-2020/metrics"
	"github.com/0xnibbler/mev-q4-2020/model"
	"github.com/0xnibbler/mev-q4-2020/scheduler"
	"github.com/0xnibbler/mev-q4-2020/tokens"
	"github.com/0xnibbler/mev-q4-2020/util"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

import _ "net/http/pprof"

var (
	flagLoad    = flag.Bool("load", true, "load new tokens (default=true)")
	flagMetrics = flag.Bool("metrics", false, "metrics (default=false)")
	flagLive    = flag.Bool("live", true, "live (default=true)")
	flagIPC     = flag.String("ipc", "", "ipc path")
)

func main() {
	flag.Parse()
	metrics.On = *flagMetrics

	go func() {
		time.Sleep(12 * time.Hour)
		panic("restart to get new tokens and pools")
	}()

	kill := make(chan os.Signal, 1)
	signal.Notify(kill, os.Interrupt, os.Kill)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-kill
		cancel()
	}()

	m := metrics.New()

	connCtx, connCancel := context.WithTimeout(ctx, time.Second)

	addr := "ws://localhost:3334"
	if *flagIPC != "" {
		addr = *flagIPC
	}
	m.Println("dialing " + addr)

	var c *rpc.Client
	var err error
	try := 0
	for {
		c, err = rpc.DialContext(connCtx, addr)
		if err != nil && try > 5 {
			m.Fatal(err)
		}
		if err == nil {
			break
		}
		try++
	}

	connCancel()

	go func() {
		for {
			sp, err := ethclient.NewClient(c).SyncProgress(context.Background())
			if err != nil {
				panic(err)
			}
			if sp != nil {
				panic("still syncing")
			}
			time.Sleep(time.Minute)
		}
	}()

	if *flagLoad {
		amm.LoadSushiswap = false
		amm.LoadUniswapV2 = false

		client := ethclient.NewClient(c)
		tl := tokens.NewList(client, m)
		p := algo.NewPrices(model.AmtThreshs, m)
		u := amm.NewUniswapV2(amm.NewConfig(c, p, tl, m))
		s := amm.NewSushiswap(amm.NewConfig(c, p, tl, m))

		if err := util.PullAll(ctx, client, tl, u, s); err != nil {
			panic(err)
		}

		amm.LoadSushiswap = true
		amm.LoadUniswapV2 = true
	}

	if err := run(ctx, c, m); err != nil {
		m.Fatal(err)
	}
}

func run(ctx context.Context, c *rpc.Client, m *metrics.Metrics) error {
	client := ethclient.NewClient(c)

	tl := tokens.NewList(client, m)

	failedAmts, err := tl.SetAmts(ctx)
	if err != nil {
		m.WithError(err).Error("SetAmts")
	}

	_ = failedAmts

	p := algo.NewPrices(model.AmtThreshs, m)
	tl.AmtGetter = p

	conf := amm.NewConfig(c, p, tl, m)

	s := amm.NewSushiswap(conf)
	u := amm.NewUniswapV2(conf)
	//crv := amm.NewCurve(conf)
	//u1 := amm.NewUniswapV1(conf)

	headCh := make(chan struct{})

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error {
		return p.Start(ctx, 200*time.Millisecond, headCh)
	})

	//erroredSync, err := u.SyncAllOld(context.Background())
	//if err != nil {
	//	return errors.Wrap(err, "uv2: SyncAll (len errs="+strconv.Itoa(len(erroredSync))+")")
	//}
	//
	//sErroredSync, err := s.SyncAllOld(context.Background())
	//if err != nil {
	//	return errors.Wrap(err, "shs: SyncAll (len errs="+strconv.Itoa(len(sErroredSync))+")")
	//}

	/*
		fmt.Println("curve get all")
		if err := crv.GetAllPools(ctx); err != nil {
			return errors.Wrap(err, "crv: GetAllPools")
		}

		fmt.Println("uv1 get all")
		if err := u1.GetAllPools(ctx); err != nil {
			return errors.Wrap(err, "uv1: GetAllPairs")
		}
	*/

	errg.Go(func() error {
		return subsHeadPrices(ctx, client, headCh, u, s)
	})

	var x *fb.Exec
	if *flagLive {
		x = &fb.Exec{
			M: fb.New(c, common.HexToAddress("")),
		}
	}
	scheduler.Live = *flagLive

	sc := scheduler.New(c, x, m)

	errg.Go(func() error {
		return errors.Wrap(sc.Start(ctx), "scheduler")
	})

	p.SetScheduler(sc)

	err = errg.Wait()
	defer m.Println("exit", err)
	return err
}
