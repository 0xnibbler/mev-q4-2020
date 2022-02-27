package metrics

import (
	"context"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"time"

	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/afiskon/promtail-client/promtail"
	"github.com/ethereum/go-ethereum/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

var (
	source_name = "arb"
	lokiPort    = ":3100"
	promPort    = ":8080"

	On = true
)

type Metrics struct {
	server *http.Server
	lHook  *logrusHook
	logrus.FieldLogger

	poolUpdates  *prometheus.CounterVec
	cycleUpdates *prometheus.GaugeVec
	gasPrice     *prometheus.GaugeVec
	cycleDur     *prometheus.GaugeVec
}

func New() *Metrics {
	m := &Metrics{
		FieldLogger: nil,
		poolUpdates: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "pools",
				Name:      "updates_total",
				Help:      "Total number of updates",
			},
			[]string{"dex_id", "pool_address"},
		),
		cycleUpdates: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "cycles",
				Name:      "cycle_return",
				Help:      "",
			},
			[]string{"len", "cycle_id", "amt"},
		),
		cycleDur: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "cycles",
				Name:      "cycle_dur",
				Help:      "",
			},
			[]string{"len", "cycle_id", "return", "amt"},
		),

		gasPrice: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "gas",
				Name:      "price",
				Help:      "",
			},
			[]string{"q"},
		),
	}

	prometheus.MustRegister(m.poolUpdates, m.cycleUpdates, m.gasPrice, m.cycleDur)

	m.Start()
	return m
}

func (m *Metrics) Start() error {
	conf := promtail.ClientConfig{
		PushURL:            "http://localhost" + lokiPort + "/api/prom/push",
		Labels:             "{source=\"" + source_name + "\"}",
		BatchWait:          5 * time.Second,
		BatchEntriesNumber: 10000,
		SendLevel:          promtail.DEBUG,
		PrintLevel:         promtail.DISABLE,
	}

	log, err := promtail.NewClientProto(conf)
	if err != nil {
		return err
	}

	logger := logrus.New()
	logger.Out = ioutil.Discard
	logger.ReportCaller = true

	m.lHook = &logrusHook{client: log}
	logger.AddHook(m.lHook)

	m.FieldLogger = logger

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	m.server = &http.Server{Addr: promPort, Handler: mux}
	go m.server.ListenAndServe()

	return nil
}

func (m *Metrics) Stop() error {
	m.lHook.client.Shutdown()
	return m.server.Shutdown(context.Background())
}

func (m *Metrics) MetricPoolUpdate(a model.AMM, p common.Address) {
	m.preMetric(func() {
		m.poolUpdates.WithLabelValues(a.String(), p.String()).Inc()
	})

}

func (m *Metrics) MetricCycle(l int, c uint64, r float64, a model.AMT) {
	m.preMetric(func() {
		m.cycleUpdates.WithLabelValues(fmt.Sprintf("%d", l), fmt.Sprintf("%d", c), a.String()).Set(r)
	})
}

func (m *Metrics) MetricCycleDur(l int, c uint64, r float64, d float64, a model.AMT) {
	m.preMetric(func() {
		cd := m.cycleDur.WithLabelValues(
			fmt.Sprintf("%d", l),
			fmt.Sprintf("%d", c),
			fmt.Sprintf("%d", int(math.Round(r*1e+5))),
			a.String())
		cd.Set(d)
		time.Sleep(time.Second * 30)
		cd.Set(0)
	})
}

func (m *Metrics) preMetric(f func()) {
	if On {
		go f()
	}
}
