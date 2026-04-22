package events

import (
	"encoding/json"
	"time"
)

// Event is the canonical envelope used for all messages published to the
// event bus. Every event carries a unique ID, a type tag for routing, a
// timestamp, and an opaque JSON payload.
type Event struct {
	// ID is a unique identifier for this event (typically a UUID).
	ID string `json:"id"`

	// Type describes the kind of event and corresponds to one of the
	// Subject* constants (e.g. "anomaly.detected").
	Type string `json:"type"`

	// Timestamp is the moment the event was created.
	Timestamp time.Time `json:"timestamp"`

	// Source identifies the component that produced the event
	// (e.g. "detector", "correlator", "reasoner").
	Source string `json:"source,omitempty"`

	// Payload carries the event-specific data as raw JSON so that
	// consumers can decode it into the appropriate concrete type.
	Payload json.RawMessage `json:"payload"`
}

// NewEvent constructs an Event by marshalling the given payload value into
// JSON. It returns an error if the payload cannot be serialised.
func NewEvent(id, eventType, source string, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        id,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Source:    source,
		Payload:   json.RawMessage(data),
	}, nil
}

// DecodePayload unmarshals the raw JSON payload into the provided target.
func (e *Event) DecodePayload(target any) error {
	return json.Unmarshal(e.Payload, target)
}
