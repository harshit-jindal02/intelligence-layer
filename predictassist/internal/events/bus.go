package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Handler is a callback invoked for each event received on a subscription.
type Handler func(ctx context.Context, event Event) error

// EventBus provides publish/subscribe access to the predictassist event
// stream backed by NATS JetStream. It manages a single connection and
// exposes typed Publish/Subscribe helpers that serialise and deserialise
// Event envelopes automatically.
type EventBus struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
}

const (
	// streamName is the JetStream stream that holds all predictassist events.
	streamName = "PREDICTASSIST"

	// defaultAckWait is how long the server waits for an ack before redelivery.
	defaultAckWait = 30 * time.Second
)

// Connect establishes a NATS connection and ensures the JetStream stream
// exists. It creates the stream if it does not already exist.
func Connect(ctx context.Context, url string, opts ...nats.Option) (*EventBus, error) {
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream init: %w", err)
	}

	// Create or update the stream to capture all predictassist subjects.
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"telemetry.>", "features.>", "anomaly.>", "correlation.>", "prediction.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("stream create: %w", err)
	}

	return &EventBus{
		conn:   nc,
		js:     js,
		stream: stream,
	}, nil
}

// Publish serialises the event and publishes it to the given NATS subject.
func (b *EventBus) Publish(ctx context.Context, subject string, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	_, err = b.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", subject, err)
	}
	return nil
}

// Subscribe creates a durable JetStream consumer on the given subject and
// dispatches incoming messages to the handler. The consumer name is derived
// from the provided consumerName argument to allow multiple independent
// subscriber groups.
//
// The returned function stops the consumer when called.
func (b *EventBus) Subscribe(
	ctx context.Context,
	subject string,
	consumerName string,
	handler Handler,
) (stop func(), err error) {
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       defaultAckWait,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", consumerName, err)
	}

	cons, err := consumer.Consume(func(msg jetstream.Msg) {
		var event Event
		if err := json.Unmarshal(msg.Data(), &event); err != nil {
			// Terminate delivery of malformed messages.
			_ = msg.Term()
			return
		}

		if err := handler(ctx, event); err != nil {
			// NAK so the message is redelivered after the ack wait.
			_ = msg.Nak()
			return
		}

		_ = msg.Ack()
	})
	if err != nil {
		return nil, fmt.Errorf("consume %s: %w", consumerName, err)
	}

	return cons.Stop, nil
}

// Close drains the NATS connection and releases all resources.
func (b *EventBus) Close() {
	if b.conn != nil {
		b.conn.Close()
	}
}
