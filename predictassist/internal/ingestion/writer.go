package ingestion

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/harshitjindal/predictassist/internal/models"
	"github.com/harshitjindal/predictassist/internal/store"
)

const (
	// DefaultBatchSize is the number of records to accumulate before flushing.
	DefaultBatchSize = 500

	// DefaultFlushInterval is the maximum time between flushes.
	DefaultFlushInterval = 5 * time.Second

	// DefaultMaxRetries is the number of retry attempts for failed writes.
	DefaultMaxRetries = 3

	// DefaultBaseBackoff is the initial backoff duration between retries.
	DefaultBaseBackoff = 500 * time.Millisecond
)

// BatchWriterConfig holds configuration for the BatchWriter.
type BatchWriterConfig struct {
	// BatchSize is the number of records to accumulate before flushing.
	BatchSize int

	// FlushInterval is the maximum duration between flushes.
	FlushInterval time.Duration

	// MaxRetries is the number of retry attempts for failed writes.
	MaxRetries int

	// BaseBackoff is the initial backoff duration between retries.
	BaseBackoff time.Duration
}

// BatchWriterMetrics tracks ingestion performance counters.
type BatchWriterMetrics struct {
	mu sync.Mutex

	// LogsIngested is the total number of log records written.
	LogsIngested int64

	// MetricsIngested is the total number of metric records written.
	MetricsIngested int64

	// TracesIngested is the total number of trace spans written.
	TracesIngested int64

	// BatchWriteDurationTotal is the cumulative duration of all batch writes.
	BatchWriteDurationTotal time.Duration

	// BatchWriteCount is the total number of batch write operations.
	BatchWriteCount int64

	// WriteErrors is the total number of failed write operations.
	WriteErrors int64
}

// BatchWriter consumes records from a TelemetrySource and writes them to
// ClickHouse in configurable batches. It retries failed writes with
// exponential backoff and tracks ingestion metrics.
type BatchWriter struct {
	source  TelemetrySource
	store   *store.ClickHouseStore
	cfg     BatchWriterConfig
	metrics BatchWriterMetrics
}

// NewBatchWriter creates a new BatchWriter that reads from the given source
// and writes to the given ClickHouse store. If cfg is nil, default values are used.
func NewBatchWriter(source TelemetrySource, chStore *store.ClickHouseStore, cfg *BatchWriterConfig) *BatchWriter {
	c := BatchWriterConfig{
		BatchSize:     DefaultBatchSize,
		FlushInterval: DefaultFlushInterval,
		MaxRetries:    DefaultMaxRetries,
		BaseBackoff:   DefaultBaseBackoff,
	}
	if cfg != nil {
		if cfg.BatchSize > 0 {
			c.BatchSize = cfg.BatchSize
		}
		if cfg.FlushInterval > 0 {
			c.FlushInterval = cfg.FlushInterval
		}
		if cfg.MaxRetries > 0 {
			c.MaxRetries = cfg.MaxRetries
		}
		if cfg.BaseBackoff > 0 {
			c.BaseBackoff = cfg.BaseBackoff
		}
	}

	return &BatchWriter{
		source: source,
		store:  chStore,
		cfg:    c,
	}
}

// Run starts consuming from all telemetry channels and writing batches to
// storage. It blocks until ctx is cancelled.
func (w *BatchWriter) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		w.consumeLogs(ctx)
	}()
	go func() {
		defer wg.Done()
		w.consumeMetrics(ctx)
	}()
	go func() {
		defer wg.Done()
		w.consumeTraces(ctx)
	}()

	wg.Wait()
	log.Printf("batch writer: all consumers stopped")
}

// Metrics returns a snapshot of the current ingestion metrics.
func (w *BatchWriter) Metrics() BatchWriterMetrics {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	return BatchWriterMetrics{
		LogsIngested:           w.metrics.LogsIngested,
		MetricsIngested:        w.metrics.MetricsIngested,
		TracesIngested:         w.metrics.TracesIngested,
		BatchWriteDurationTotal: w.metrics.BatchWriteDurationTotal,
		BatchWriteCount:        w.metrics.BatchWriteCount,
		WriteErrors:            w.metrics.WriteErrors,
	}
}

// consumeLogs batches log records and flushes them to ClickHouse.
func (w *BatchWriter) consumeLogs(ctx context.Context) {
	batch := make([]models.LogRecord, 0, w.cfg.BatchSize)
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toWrite := make([]models.LogRecord, len(batch))
		copy(toWrite, batch)
		batch = batch[:0]

		w.writeWithRetry(ctx, "logs", func(ctx context.Context) error {
			return w.store.InsertLogs(ctx, toWrite)
		}, int64(len(toWrite)))

		w.metrics.mu.Lock()
		w.metrics.LogsIngested += int64(len(toWrite))
		w.metrics.mu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec, ok := <-w.source.Logs():
			if !ok {
				flush()
				return
			}
			batch = append(batch, *rec)
			if len(batch) >= w.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// consumeMetrics batches metric records and flushes them to ClickHouse.
func (w *BatchWriter) consumeMetrics(ctx context.Context) {
	batch := make([]models.MetricRecord, 0, w.cfg.BatchSize)
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toWrite := make([]models.MetricRecord, len(batch))
		copy(toWrite, batch)
		batch = batch[:0]

		w.writeWithRetry(ctx, "metrics", func(ctx context.Context) error {
			return w.store.InsertMetrics(ctx, toWrite)
		}, int64(len(toWrite)))

		w.metrics.mu.Lock()
		w.metrics.MetricsIngested += int64(len(toWrite))
		w.metrics.mu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec, ok := <-w.source.Metrics():
			if !ok {
				flush()
				return
			}
			batch = append(batch, *rec)
			if len(batch) >= w.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// consumeTraces batches trace spans and flushes them to ClickHouse.
func (w *BatchWriter) consumeTraces(ctx context.Context) {
	batch := make([]models.TraceSpan, 0, w.cfg.BatchSize)
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toWrite := make([]models.TraceSpan, len(batch))
		copy(toWrite, batch)
		batch = batch[:0]

		w.writeWithRetry(ctx, "traces", func(ctx context.Context) error {
			return w.store.InsertTraces(ctx, toWrite)
		}, int64(len(toWrite)))

		w.metrics.mu.Lock()
		w.metrics.TracesIngested += int64(len(toWrite))
		w.metrics.mu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec, ok := <-w.source.Traces():
			if !ok {
				flush()
				return
			}
			batch = append(batch, *rec)
			if len(batch) >= w.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// writeWithRetry attempts a write operation with exponential backoff on failure.
func (w *BatchWriter) writeWithRetry(ctx context.Context, dataType string, writeFn func(context.Context) error, count int64) {
	start := time.Now()
	var err error

	for attempt := 0; attempt <= w.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := w.cfg.BaseBackoff * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				log.Printf("batch writer: context cancelled during %s retry", dataType)
				return
			case <-time.After(backoff):
			}
			log.Printf("batch writer: retrying %s write (attempt %d/%d)", dataType, attempt+1, w.cfg.MaxRetries)
		}

		err = writeFn(ctx)
		if err == nil {
			duration := time.Since(start)
			w.metrics.mu.Lock()
			w.metrics.BatchWriteDurationTotal += duration
			w.metrics.BatchWriteCount++
			w.metrics.mu.Unlock()

			log.Printf("batch writer: wrote %d %s records in %v", count, dataType, duration)
			return
		}

		w.metrics.mu.Lock()
		w.metrics.WriteErrors++
		w.metrics.mu.Unlock()
		log.Printf("batch writer: %s write error (attempt %d): %v", dataType, attempt+1, err)
	}

	log.Printf("batch writer: %s write failed after %d retries: %v",
		dataType, w.cfg.MaxRetries, fmt.Errorf("final error: %w", err))
}
