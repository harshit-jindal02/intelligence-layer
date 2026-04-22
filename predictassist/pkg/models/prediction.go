package models

import "time"

// IncidentFingerprint uniquely identifies a class of predicted incidents
// so that duplicate predictions can be suppressed and trends tracked.
type IncidentFingerprint struct {
	// ServiceName is the primary service involved.
	ServiceName string `json:"service_name"`

	// Category classifies the incident type (e.g. "latency_spike",
	// "error_surge", "resource_exhaustion").
	Category string `json:"category"`

	// Hash is a deterministic hash of the contributing anomaly features,
	// used for deduplication.
	Hash string `json:"hash"`
}

// AnomalyRule describes a condition or pattern that was matched during
// anomaly correlation and fed into the prediction engine.
type AnomalyRule struct {
	// Name is the rule identifier (e.g. "sustained_error_rate_spike").
	Name string `json:"name"`

	// Description explains what the rule detects.
	Description string `json:"description"`

	// Condition is a human-readable expression of the trigger condition.
	Condition string `json:"condition"`

	// Matched indicates whether the rule was satisfied.
	Matched bool `json:"matched"`
}

// Insight is a single actionable recommendation produced by the reasoner
// to help operators prevent or mitigate a predicted incident.
type Insight struct {
	// Summary is a one-line description of the insight.
	Summary string `json:"summary"`

	// Detail provides an extended explanation with supporting evidence.
	Detail string `json:"detail,omitempty"`

	// Confidence is the model's confidence in this insight, from 0 to 1.
	Confidence float64 `json:"confidence"`

	// SuggestedAction describes a concrete step the operator can take.
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// IncidentPrediction is the top-level output of the prediction pipeline.
// It aggregates correlated anomalies, matched rules, and reasoner insights
// into a single actionable prediction.
type IncidentPrediction struct {
	// ID is the unique identifier for this prediction.
	ID string `json:"id"`

	// Fingerprint groups this prediction with others of the same class.
	Fingerprint IncidentFingerprint `json:"fingerprint"`

	// PredictedAt is when the prediction was generated.
	PredictedAt time.Time `json:"predicted_at"`

	// Probability is the estimated likelihood of the incident occurring,
	// expressed as a value between 0 and 1.
	Probability float64 `json:"probability"`

	// Severity is the predicted severity if the incident materialises.
	Severity AnomalySeverity `json:"severity"`

	// TimeToIncident is the estimated lead time before the incident occurs.
	TimeToIncident string `json:"time_to_incident,omitempty"`

	// AnomalyIDs lists the anomaly events that contributed to this prediction.
	AnomalyIDs []string `json:"anomaly_ids"`

	// MatchedRules lists the anomaly rules that were satisfied.
	MatchedRules []AnomalyRule `json:"matched_rules"`

	// Insights holds actionable recommendations from the reasoner.
	Insights []Insight `json:"insights,omitempty"`

	// AffectedServices lists all services expected to be impacted.
	AffectedServices []string `json:"affected_services"`

	// Labels holds additional metadata.
	Labels map[string]string `json:"labels,omitempty"`
}
