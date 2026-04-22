// Package ingestion provides telemetry data ingestion from OTLP sources into
// PredictAssist's internal storage layer.
package ingestion

import (
	"context"

	"github.com/harshitjindal/predictassist/internal/models"
)

// TelemetrySource defines the interface for any telemetry data source that
// produces logs, metrics, and trace spans. Implementations must be safe for
// concurrent use after Start returns.
type TelemetrySource interface {
	// Start begins receiving telemetry data. It blocks until the source is
	// ready to emit records or ctx is cancelled.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the source, draining any in-flight data.
	Stop(ctx context.Context) error

	// Logs returns a read-only channel of ingested log records.
	Logs() <-chan *models.LogRecord

	// Metrics returns a read-only channel of ingested metric records.
	Metrics() <-chan *models.MetricRecord

	// Traces returns a read-only channel of ingested trace spans.
	Traces() <-chan *models.TraceSpan
}
