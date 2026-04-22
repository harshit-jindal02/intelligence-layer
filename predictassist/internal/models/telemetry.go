// Package models defines the canonical internal representations for telemetry
// data ingested by PredictAssist.
package models

import "time"

// LogRecord is the canonical internal representation of a log entry.
type LogRecord struct {
	Timestamp          time.Time         `json:"timestamp"`
	Service            string            `json:"service"`
	Severity           string            `json:"severity"`
	Body               string            `json:"body"`
	Attributes         map[string]string `json:"attributes"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
	TraceID            string            `json:"trace_id,omitempty"`
	SpanID             string            `json:"span_id,omitempty"`
}

// MetricRecord is the canonical internal representation of a metric data point.
type MetricRecord struct {
	Timestamp          time.Time         `json:"timestamp"`
	Service            string            `json:"service"`
	Name               string            `json:"name"`
	Value              float64           `json:"value"`
	MetricType         string            `json:"metric_type"`
	Attributes         map[string]string `json:"attributes"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
}

// TraceSpan is the canonical internal representation of a distributed trace span.
type TraceSpan struct {
	Timestamp          time.Time         `json:"timestamp"`
	Service            string            `json:"service"`
	TraceID            string            `json:"trace_id"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	Operation          string            `json:"operation"`
	DurationNs         int64             `json:"duration_ns"`
	Status             string            `json:"status"`
	Attributes         map[string]string `json:"attributes"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
}
