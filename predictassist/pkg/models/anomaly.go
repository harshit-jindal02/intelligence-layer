package models

import "time"

// AnomalySeverity classifies how critical an anomaly is.
type AnomalySeverity string

const (
	// SeverityLow indicates a minor deviation that may not require action.
	SeverityLow AnomalySeverity = "low"

	// SeverityMedium indicates a notable deviation worth investigating.
	SeverityMedium AnomalySeverity = "medium"

	// SeverityHigh indicates a significant deviation likely impacting users.
	SeverityHigh AnomalySeverity = "high"

	// SeverityCritical indicates a severe deviation requiring immediate attention.
	SeverityCritical AnomalySeverity = "critical"
)

// AnomalyFeature captures a single feature that contributed to an anomaly
// detection, including its observed and expected values.
type AnomalyFeature struct {
	// Name is the feature identifier (e.g. "error_rate", "latency_p99").
	Name string `json:"name"`

	// Value is the observed value at detection time.
	Value float64 `json:"value"`

	// Baseline is the expected value derived from the baseline window.
	Baseline float64 `json:"baseline"`

	// ZScore is the number of standard deviations from the baseline mean.
	ZScore float64 `json:"z_score"`

	// StdDev is the standard deviation of the baseline window.
	StdDev float64 `json:"std_dev"`
}

// AnomalyEvent represents a detected anomaly for a specific service. It
// bundles the contributing features, severity assessment, and metadata
// needed for downstream correlation and prediction.
type AnomalyEvent struct {
	// ID is the unique identifier for this anomaly event.
	ID string `json:"id"`

	// ServiceName is the service where the anomaly was detected.
	ServiceName string `json:"service_name"`

	// DetectedAt is the timestamp when the anomaly was detected.
	DetectedAt time.Time `json:"detected_at"`

	// Severity is the assessed criticality of the anomaly.
	Severity AnomalySeverity `json:"severity"`

	// Features lists the individual feature deviations that triggered
	// the anomaly.
	Features []AnomalyFeature `json:"features"`

	// Window is the time window over which the anomaly was evaluated
	// (e.g. "5m").
	Window string `json:"window"`

	// Description is a human-readable summary of the anomaly.
	Description string `json:"description,omitempty"`

	// Labels holds additional classification labels (e.g. detector type).
	Labels map[string]string `json:"labels,omitempty"`
}
