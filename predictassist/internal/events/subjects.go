// Package events provides NATS JetStream-based event bus primitives for
// publishing and subscribing to telemetry, feature, and prediction events
// within the predictassist system.
package events

// NATS subject constants follow a dot-separated hierarchy so that consumers
// can subscribe to broad categories (e.g. "telemetry.>") or specific
// signal types.

const (
	// --- Telemetry ingestion subjects ---

	// SubjectTelemetryLogs is published when new log records are ingested.
	SubjectTelemetryLogs = "telemetry.logs"

	// SubjectTelemetryMetrics is published when new metric data points are ingested.
	SubjectTelemetryMetrics = "telemetry.metrics"

	// SubjectTelemetryTraces is published when new trace spans are ingested.
	SubjectTelemetryTraces = "telemetry.traces"

	// --- Feature computation subjects ---

	// SubjectFeaturesComputed is published when a new feature vector is
	// computed for a service window.
	SubjectFeaturesComputed = "features.computed"

	// SubjectFeaturesExpired is published when a feature window expires
	// and its data is flushed.
	SubjectFeaturesExpired = "features.expired"

	// --- Anomaly detection subjects ---

	// SubjectAnomalyDetected is published when an anomaly is detected.
	SubjectAnomalyDetected = "anomaly.detected"

	// SubjectAnomalyResolved is published when a previously active anomaly
	// returns to normal.
	SubjectAnomalyResolved = "anomaly.resolved"

	// --- Correlation subjects ---

	// SubjectCorrelationGroup is published when multiple anomalies are
	// correlated into a single group.
	SubjectCorrelationGroup = "correlation.group"

	// --- Prediction subjects ---

	// SubjectPredictionCreated is published when a new incident prediction
	// is generated.
	SubjectPredictionCreated = "prediction.created"

	// SubjectPredictionUpdated is published when an existing prediction is
	// revised with new evidence.
	SubjectPredictionUpdated = "prediction.updated"

	// SubjectPredictionClosed is published when a prediction is closed,
	// either because the incident occurred or the risk subsided.
	SubjectPredictionClosed = "prediction.closed"
)
