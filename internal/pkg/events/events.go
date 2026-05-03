package events

import (
	"context"
	"sync"
	"time"
)

const (
	TopicChannelChanged = "channel.changed"
	TopicConfigChanged  = "config.changed"
	TopicQuotaReserved  = "billing.quota.reserved"
	TopicQuotaCommitted = "billing.quota.committed"
	TopicQuotaReleased  = "billing.quota.released"
	TopicRelayFailed    = "relay.request.failed"
	TopicRelayFinished  = "relay.request.finished"
)

// Event represents a domain event.
type Event struct {
	Topic     string
	Payload   interface{}
	Timestamp time.Time
}

// Handler processes an event.
type Handler func(ctx context.Context, event Event) error

// EventBus defines the interface for publishing and subscribing to events.
type EventBus interface {
	Publish(ctx context.Context, topic string, payload interface{}) error
	Subscribe(topic string, handler Handler)
}

// MemoryEventBus is a channel-based in-process EventBus implementation.
type MemoryEventBus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewMemoryEventBus creates a new MemoryEventBus.
func NewMemoryEventBus() *MemoryEventBus {
	return &MemoryEventBus{
		handlers: make(map[string][]Handler),
	}
}

func (b *MemoryEventBus) Publish(ctx context.Context, topic string, payload interface{}) error {
	b.mu.RLock()
	handlers := b.handlers[topic]
	b.mu.RUnlock()

	event := Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	}
	for _, h := range handlers {
		// fire-and-forget; callers can handle errors via their own logging
		_ = h(ctx, event)
	}
	return nil
}

func (b *MemoryEventBus) Subscribe(topic string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], handler)
}
