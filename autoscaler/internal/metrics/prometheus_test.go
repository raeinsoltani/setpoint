package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func quietOpts(url, query string) PrometheusOptions {
	return PrometheusOptions{
		URL:     url,
		Query:   query,
		Timeout: 2 * time.Second,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// serveJSON returns a stub Prometheus answering /api/v1/query with a fixed body.
func serveJSON(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadResultTypes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{
			name: "single-element vector",
			body: `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{},"value":[1700000000,"142.5"]}]}}`,
			want: 142.5,
		},
		{
			// No pods reporting yet. Zero load is the correct reading — treating
			// it as an error would make the autoscaler hold at whatever replica
			// count it had when the target was last up.
			name: "empty vector is zero, not an error",
			body: `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			want: 0,
		},
		{
			name: "scalar",
			body: `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"7"]}}`,
			want: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewPrometheus(quietOpts(serveJSON(t, tt.body).URL, "up"))
			if err != nil {
				t.Fatalf("NewPrometheus: %v", err)
			}
			got, err := c.Read(context.Background())
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got != tt.want {
				t.Errorf("Read() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A query returning several series is a configuration mistake. Taking the first
// would silently scale on one arbitrary pod's metric, which is both wrong and
// very hard to notice on a dashboard — so it must fail loudly instead.
func TestMultiSeriesResultIsRejected(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"pod":"a"},"value":[1700000000,"100"]},
		{"metric":{"pod":"b"},"value":[1700000000,"900"]}]}}`

	c, err := NewPrometheus(quietOpts(serveJSON(t, body).URL, "rate(http_requests_total[1m])"))
	if err != nil {
		t.Fatalf("NewPrometheus: %v", err)
	}
	if _, err := c.Read(context.Background()); !errors.Is(err, ErrAmbiguousResult) {
		t.Errorf("Read error = %v, want ErrAmbiguousResult", err)
	}
}

// A malformed query fails identically on every attempt, so retrying it only
// burns the reconcile budget.
func TestAmbiguousResultIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"pod":"a"},"value":[1700000000,"1"]},
			{"metric":{"pod":"b"},"value":[1700000000,"2"]}]}}`)
	}))
	defer srv.Close()

	opts := quietOpts(srv.URL, "up")
	opts.Retries = 3
	c, _ := NewPrometheus(opts)
	if _, err := c.Read(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server hit %d times, want 1 — a deterministic failure must not be retried", n)
	}
}

// Prometheus restarting mid-scrape is the common transient case: the retry must
// absorb it rather than costing a whole reconcile interval.
func TestReadRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{},"value":[1700000000,"55"]}]}}`)
	}))
	defer srv.Close()

	opts := quietOpts(srv.URL, "up")
	opts.Retries = 2
	c, _ := NewPrometheus(opts)

	got, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != 55 {
		t.Errorf("Read() = %v, want 55", got)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("server hit %d times, want 2", n)
	}
}

func TestReadFailsAfterExhaustingRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	opts := quietOpts(srv.URL, "up")
	opts.Retries = 1
	c, _ := NewPrometheus(opts)
	if _, err := c.Read(context.Background()); err == nil {
		t.Error("expected an error once retries were exhausted")
	}
}

// A cancelled context must abort immediately rather than sleeping out the
// backoff, so shutdown is not delayed by a retry in flight.
func TestReadHonoursCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	opts := quietOpts(srv.URL, "up")
	opts.Retries = 5
	c, _ := NewPrometheus(opts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := c.Read(ctx); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Read took %v on a cancelled context; it slept through the backoff", elapsed)
	}
}

func TestNewPrometheusRequiresQuery(t *testing.T) {
	if _, err := NewPrometheus(PrometheusOptions{URL: "http://localhost:9090"}); err == nil {
		t.Error("NewPrometheus accepted an empty query")
	}
}

func TestStaticCollector(t *testing.T) {
	c := NewStatic(12)
	if got, _ := c.Read(context.Background()); got != 12 {
		t.Errorf("Read() = %v, want 12", got)
	}
	c.Set(34)
	if got, _ := c.Read(context.Background()); got != 34 {
		t.Errorf("Read() after Set = %v, want 34", got)
	}
}
