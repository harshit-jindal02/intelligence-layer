// Package models defines the canonical internal data types used throughout
// the predictassist system for telemetry ingestion, feature computation,
// anomaly detection, and incident prediction.
package models

import "time"

// LogRecord represents a single log entry ingested from an OpenTelemetry
// log source. It carries the original body, severity, and a flattened
// attribute map for downstream feature extraction.
type LogRecord struct {
	// Timestamp is the moment the log was emitted at the source.
	Timestamp time.Time `json:"timestamp" ch:"timestamp"`

	// ServiceName identifies the originating service.
	ServiceName string `json:"service_name" ch:"service_name"`

	// SeverityText is the human-readable severity (e.g. "ERROR", "WARN").
	SeverityText string `json:"severity_text" ch:"severity_text"`

	// SeverityNumber is the numeric severity as defined by the OTel spec.
	SeverityNumber int32 `json:"severity_number" ch:"severity_number"`

	// Body is the raw log message content.
	Body string `json:"body" ch:"body"`

	// TraceID links the log to a distributed trace, if available.
	TraceID string `json:"trace_id,omitempty" ch:"trace_id"`

	// SpanID links the log to a specific span, if available.
	SpanID string `json:"span_id,omitempty" ch:"span_id"`

	// Attributes holds arbitrary key-value metadata attached to the log.
	Attributes map[string]string `json:"attributes,omitempty" ch:"attributes"`

	// ResourceAttributes holds resource-level metadata (e.g. host, k8s pod).
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty" ch:"resource_attributes"`
}

// MetricRecord represents a single metric data point ingested from an
// OpenTelemetry metric source.
type MetricRecord struct {
	// Timestamp is the moment the metric was observed.
	Timestamp time.Time `json:"timestamp" ch:"timestamp"`

	// ServiceName identifies the originating service.
	ServiceName string `json:"service_name" ch:"service_name"`

	// Name is the metric instrument name (e.g. "http.server.duration").
	Name string `json:"name" ch:"name"`

	// Description provides a human-readable explanation of the metric.
	Description string `json:"description,omitempty" ch:"description"`

	// Unit is the metric unit (e.g. "ms", "By", "1").
	Unit string `json:"unit,omitempty" ch:"unit"`

	// Value is the observed numeric value of the data point.
	Value float64 `json:"value" ch:"value"`

	// Attributes holds dimension key-value pairs for the data point.
	Attributes map[string]string `json:"attributes,omitempty" ch:"attributes"`

	// ResourceAttributes holds resource-level metadata.
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty" ch:"resource_attributes"`
}

// TraceSpan represents a single span within a distributed trace, ingested
// from an OpenTelemetry trace source.
type TraceSpan struct {
	// TraceID is the 128-bit trace identifier encoded as a hex string.
	TraceID string `json:"trace_id" ch:"trace_id"`

	// SpanID is the 64-bit span identifier encoded as a hex string.
	SpanID string `json:"span_id" ch:"span_id"`

	// ParentSpanID is the span ID of the parent, empty for root spans.
	ParentSpanID string `json:"parent_span_id,omitempty" ch:"parent_span_id"`

	// ServiceName identifies the originating service.
	ServiceName string `json:"service_name" ch:"service_name"`

	// OperationName is the logical name of the operation (e.g. "GET /api/orders").
	OperationName string `json:"operation_name" ch:"operation_name"`

	// Kind indicates the span kind (client, server, producer, consumer, internal).
	Kind string `json:"kind" ch:"kind"`

	// StartTime is when the span started.
	StartTime time.Time `json:"start_time" ch:"start_time"`

	// EndTime is when the span ended.
	EndTime time.Time `json:"end_time" ch:"end_time"`

	// DurationMs is the span duration in milliseconds.
	DurationMs float64 `json:"duration_ms" ch:"duration_ms"`

	// StatusCode is the OTel status code (Unset, Ok, Error).
	StatusCode string `json:"status_code" ch:"status_code"`

	// StatusMessage is an optional message associated with the status.
	StatusMessage string `json:"status_message,omitempty" ch:"status_message"`

	// Attributes holds span-level key-value metadata.
	Attributes map[string]string `json:"attributes,omitempty" ch:"attributes"`

	// ResourceAttributes holds resource-level metadata.
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty" ch:"resource_attributes"`

	// Events holds span events (e.g. exceptions, annotations).
	Events []SpanEvent `json:"events,omitempty" ch:"events"`
}

// SpanEvent is a timestamped annotation within a span.
type SpanEvent struct {
	// Name is the event name (e.g. "exception").
	Name string `json:"name"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// Attributes holds event-level key-value metadata.
	Attributes map[string]string `json:"attributes,omitempty"`
}
