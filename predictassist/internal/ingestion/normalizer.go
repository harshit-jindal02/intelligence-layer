package ingestion

import (
	"time"

	"github.com/harshitjindal/predictassist/internal/models"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// NormalizeLogs converts OTLP ResourceLogs into the canonical internal
// LogRecord representation. It extracts the service name from resource
// attributes and flattens the nested OTLP structure.
func NormalizeLogs(resourceLogs []*logspb.ResourceLogs) []models.LogRecord {
	var records []models.LogRecord

	for _, rl := range resourceLogs {
		resAttrs := extractAttributes(rl.GetResource())
		service := extractServiceName(rl.GetResource())

		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				rec := models.LogRecord{
					Timestamp:          timeFromUnixNano(lr.GetTimeUnixNano()),
					Service:            service,
					Severity:           lr.GetSeverityText(),
					Body:               lr.GetBody().GetStringValue(),
					Attributes:         kvListToMap(lr.GetAttributes()),
					ResourceAttributes: resAttrs,
					TraceID:            hexEncode(lr.GetTraceId()),
					SpanID:             hexEncode(lr.GetSpanId()),
				}
				records = append(records, rec)
			}
		}
	}

	return records
}

// NormalizeMetrics converts OTLP ResourceMetrics into the canonical internal
// MetricRecord representation. Each data point within a metric becomes a
// separate MetricRecord.
func NormalizeMetrics(resourceMetrics []*metricspb.ResourceMetrics) []models.MetricRecord {
	var records []models.MetricRecord

	for _, rm := range resourceMetrics {
		resAttrs := extractAttributes(rm.GetResource())
		service := extractServiceName(rm.GetResource())

		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				name := m.GetName()
				records = append(records, extractDataPoints(m, name, service, resAttrs)...)
			}
		}
	}

	return records
}

// NormalizeTraces converts OTLP ResourceSpans into the canonical internal
// TraceSpan representation.
func NormalizeTraces(resourceSpans []*tracepb.ResourceSpans) []models.TraceSpan {
	var spans []models.TraceSpan

	for _, rs := range resourceSpans {
		resAttrs := extractAttributes(rs.GetResource())
		service := extractServiceName(rs.GetResource())

		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				span := models.TraceSpan{
					Timestamp:          timeFromUnixNano(s.GetStartTimeUnixNano()),
					Service:            service,
					TraceID:            hexEncode(s.GetTraceId()),
					SpanID:             hexEncode(s.GetSpanId()),
					ParentSpanID:       hexEncode(s.GetParentSpanId()),
					Operation:          s.GetName(),
					DurationNs:         int64(s.GetEndTimeUnixNano() - s.GetStartTimeUnixNano()),
					Status:             s.GetStatus().GetCode().String(),
					Attributes:         kvListToMap(s.GetAttributes()),
					ResourceAttributes: resAttrs,
				}
				spans = append(spans, span)
			}
		}
	}

	return spans
}

// extractDataPoints pulls individual data points from an OTLP metric, handling
// gauge, sum, and histogram metric types.
func extractDataPoints(m *metricspb.Metric, name, service string, resAttrs map[string]string) []models.MetricRecord {
	var records []models.MetricRecord

	switch data := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.GetDataPoints() {
			records = append(records, newMetricRecord(
				dp.GetTimeUnixNano(), name, service, "gauge",
				numberValue(dp), kvListToMap(dp.GetAttributes()), resAttrs,
			))
		}
	case *metricspb.Metric_Sum:
		for _, dp := range data.Sum.GetDataPoints() {
			records = append(records, newMetricRecord(
				dp.GetTimeUnixNano(), name, service, "sum",
				numberValue(dp), kvListToMap(dp.GetAttributes()), resAttrs,
			))
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.GetDataPoints() {
			records = append(records, newMetricRecord(
				dp.GetTimeUnixNano(), name, service, "histogram",
				dp.GetSum(), kvListToMap(dp.GetAttributes()), resAttrs,
			))
		}
	case *metricspb.Metric_Summary:
		for _, dp := range data.Summary.GetDataPoints() {
			records = append(records, newMetricRecord(
				dp.GetTimeUnixNano(), name, service, "summary",
				dp.GetSum(), kvListToMap(dp.GetAttributes()), resAttrs,
			))
		}
	}

	return records
}

// numberDataPoint is satisfied by both gauge and sum data points.
type numberDataPoint interface {
	GetAsDouble() float64
	GetAsInt() int64
}

// numberValue extracts the numeric value from an OTLP NumberDataPoint,
// preferring double over int.
func numberValue(dp numberDataPoint) float64 {
	if v := dp.GetAsDouble(); v != 0 {
		return v
	}
	return float64(dp.GetAsInt())
}

func newMetricRecord(timeNano uint64, name, service, metricType string, value float64, attrs, resAttrs map[string]string) models.MetricRecord {
	return models.MetricRecord{
		Timestamp:          timeFromUnixNano(timeNano),
		Service:            service,
		Name:               name,
		Value:              value,
		MetricType:         metricType,
		Attributes:         attrs,
		ResourceAttributes: resAttrs,
	}
}

// extractServiceName retrieves the service.name attribute from an OTLP resource.
func extractServiceName(res *resourcepb.Resource) string {
	if res == nil {
		return "unknown"
	}
	for _, attr := range res.GetAttributes() {
		if attr.GetKey() == "service.name" {
			return attr.GetValue().GetStringValue()
		}
	}
	return "unknown"
}

// extractAttributes converts all resource attributes to a flat string map.
func extractAttributes(res *resourcepb.Resource) map[string]string {
	if res == nil {
		return map[string]string{}
	}
	return kvListToMap(res.GetAttributes())
}

// kvListToMap converts a slice of OTLP KeyValue pairs to a Go string map.
func kvListToMap(kvs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		m[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return m
}

// timeFromUnixNano converts a nanosecond Unix timestamp to time.Time.
func timeFromUnixNano(ns uint64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(ns)).UTC()
}
