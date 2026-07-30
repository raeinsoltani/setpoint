// Command sample-app is the workload the autoscaler scales during evaluation.
//
// It is not a realistic microservice and does not try to be. What it has to be is
// *calibrated*: a service whose CPU cost per request is known, so that the three
// comparison arms in Phase 6 measure the same thing.
//
// The calibration argument, which is load-bearing for the whole evaluation:
//
//	target throughput  = 100 req/s per replica   (policy.target in config.yaml)
//	CPU cost per req   = WORK_CPU_MS = 2ms
//	CPU at target      = 100 x 2ms = 200ms/s = 200m
//	CPU request        = 200m       (deploy/sample-app/deployment.yaml)
//
// So a replica serving exactly its target throughput sits at exactly 100% of its
// CPU request. That single equality is what makes `hpa-cpu` (HPA on CPU at 100%
// utilization) and `ours-threshold` (our policy on req/s at target 100) describe
// the *same* setpoint. Without it the two arms would be scaling to different
// operating points and any difference between them would be meaningless.
//
// It also means the work must be genuinely CPU-bound. A service that slept instead
// of computing would show flat CPU under rising load, the `hpa-cpu` arm would never
// react, and the baseline would be a straw man.
//
// Go 1.25 onwards sets GOMAXPROCS from the cgroup CPU limit, so the container's
// limit is respected without setting it by hand.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled, by path and status.",
	}, []string{"path", "status"})

	duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "HTTP request latency.",
		// Buckets straddle the latencies this app is configured to produce, so the
		// p95/p99 the evaluation reports are not interpolated across a huge bucket.
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"path"})

	inFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Requests currently being served.",
	})
)

// sink defeats dead-code elimination in the CPU burner: without an observable
// effect the compiler is free to delete the loop, and the app would silently
// consume no CPU — which would look like a scaling bug rather than a build artefact.
var sink float64

func main() {
	var (
		addr    = flag.String("addr", ":8080", "listen address")
		cpuMS   = flag.Float64("cpu-ms", envFloat("WORK_CPU_MS", 2), "CPU milliseconds burned per request")
		sleepMS = flag.Float64("sleep-ms", envFloat("WORK_SLEEP_MS", 0), "non-CPU latency added per request")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	registry := prometheus.NewRegistry()
	registry.MustRegister(requests, duration, inFlight)
	// Process and Go collectors give the report a second, independent view of CPU
	// use to cross-check the calibration above against what the container actually
	// burns.
	registry.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", work(*cpuMS, *sleepMS))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("sample-app listening",
			slog.String("addr", *addr),
			slog.Float64("cpu_ms_per_request", *cpuMS),
			slog.Float64("sleep_ms_per_request", *sleepMS))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	// Drain in-flight requests so a scale-down during a load run does not show up
	// as k6 errors and pollute the latency figures.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", slog.Any("error", err))
	}
}

// work handles a request by burning a known amount of CPU, optionally adding
// non-CPU latency. Both are overridable per request via ?cpu_ms= and ?sleep_ms=,
// which is how the per-replica capacity is probed when calibrating.
func work(defaultCPUMS, defaultSleepMS float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		inFlight.Inc()
		defer inFlight.Dec()

		cpuMS, sleepMS := defaultCPUMS, defaultSleepMS
		if v, err := strconv.ParseFloat(r.URL.Query().Get("cpu_ms"), 64); err == nil && v >= 0 {
			cpuMS = v
		}
		if v, err := strconv.ParseFloat(r.URL.Query().Get("sleep_ms"), 64); err == nil && v >= 0 {
			sleepMS = v
		}

		burnCPU(time.Duration(cpuMS * float64(time.Millisecond)))
		if sleepMS > 0 {
			select {
			case <-time.After(time.Duration(sleepMS * float64(time.Millisecond))):
			case <-r.Context().Done():
				// The client gave up. Record it rather than pretending it succeeded.
				requests.WithLabelValues("/", "499").Inc()
				return
			}
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")

		requests.WithLabelValues("/", "200").Inc()
		duration.WithLabelValues("/").Observe(time.Since(start).Seconds())
	}
}

// burnCPU spins for approximately d of CPU time.
//
// The clock is checked every 1024 iterations rather than every iteration: at 2ms of
// work a per-iteration time.Now() would be a large fraction of the total cost, so
// the app would spend its budget reading the clock instead of simulating work.
func burnCPU(d time.Duration) {
	if d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	x := 1.0001
	for {
		for range 1024 {
			x *= 1.0000001
			if x > 1e6 {
				x = 1.0001
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	sink = x
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return fallback
}
