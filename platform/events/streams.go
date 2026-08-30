package events

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"micro-one-api/pkg/jsonx"
)

const (
	// DefaultStreamMaxLen is the default maximum length for streams.
	DefaultStreamMaxLen = 10000
	// DefaultConsumerGroup is the prefix used to derive a per-service consumer
	// group name. Each service joins its OWN group (micro-one-api:<serviceName>) so
	// every service receives an independent copy of every event (broadcast
	// semantics) instead of load-balancing one copy across all services
	// (platform-M1: the shared group silently dropped cross-service events).
	DefaultConsumerGroup = "micro-one-api"
)

// StreamEventBus is a cross-process EventBus backed by Redis Streams.
// It guarantees at-least-once delivery with consumer groups.
type StreamEventBus struct {
	redis         *redis.Client
	consumerID    string
	consumerGroup string
	handlers      map[string][]Handler
	handlersMu    sync.RWMutex
	maxlen        int64
	readTimeout   time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closed        bool
	mu            sync.Mutex
}

// NewStreamEventBus creates a new Redis Streams-based event bus.
func NewStreamEventBus(redisClient *redis.Client, consumerID string) *StreamEventBus {
	ctx, cancel := context.WithCancel(context.Background())

	return &StreamEventBus{
		redis:         redisClient,
		consumerID:    consumerID,
		consumerGroup: deriveConsumerGroup(consumerID),
		handlers:      make(map[string][]Handler),
		maxlen:        DefaultStreamMaxLen,
		readTimeout:   5 * time.Second,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// deriveConsumerGroup returns the per-service consumer group name. The consumerID
// is already service-specific (passed by NewConfiguredEventBus from each service),
// so embedding it in the group name gives each service its own copy of every
// event (platform-M1). We keep the "micro-one-api:" namespace prefix for
// discoverability in XINFO GROUPS output.
func deriveConsumerGroup(consumerID string) string {
	if consumerID == "" {
		consumerID = "default"
	}
	return DefaultConsumerGroup + ":" + consumerID
}

// Publish sends an event to a Redis Stream with guaranteed persistence.
// Events survive process restarts.
func (b *StreamEventBus) Publish(ctx context.Context, topic string, payload any) error {
	data, err := jsonx.Marshal(Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	err = b.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		MaxLen: b.maxlen,
		Approx: true,
		Values: map[string]any{
			"payload":   string(data),
			"timestamp": time.Now().UnixNano(),
			"producer":  b.consumerID,
		},
	}).Err()

	if err != nil {
		return fmt.Errorf("publish event to stream %s: %w", topic, err)
	}

	return nil
}

// Subscribe joins a consumer group and processes events.
// Each event is ACKed only after the handler succeeds.
func (b *StreamEventBus) Subscribe(topic string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.handlersMu.Lock()
	b.handlers[topic] = append(b.handlers[topic], handler)
	b.handlersMu.Unlock()

	// Ensure consumer group exists
	if err := b.ensureGroup(b.ctx, topic); err != nil {
		// Log error but don't fail - will retry on consume
		fmt.Printf("failed to ensure consumer group for %s: %v\n", topic, err)
	}

	// Start consume loop if not already running for this topic
	b.wg.Add(1)
	go b.consumeLoop(topic)
}

// consumeLoop continuously reads and processes events from a stream.
func (b *StreamEventBus) consumeLoop(topic string) {
	defer b.wg.Done()

	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		// Read new messages through the consumer group created in Subscribe.
		// XReadGroup is required here; XRead would deliver messages outside the
		// group while the later XAck would acknowledge a message that was never
		// assigned to this consumer.
		msgs, err := b.redis.XReadGroup(b.ctx, &redis.XReadGroupArgs{
			Group:    b.consumerGroup,
			Consumer: b.consumerID,
			Streams:  []string{topic, ">"},
			Count:    10,
			Block:    b.readTimeout,
		}).Result()

		if err != nil {
			if err == redis.Nil {
				// No new messages, continue
				continue
			}
			// Log error and continue
			fmt.Printf("error reading from stream %s: %v\n", topic, err)
			time.Sleep(time.Second)
			continue
		}

		// Process messages
		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				b.processMessage(topic, &msg)
			}
		}
	}
}

// processMessage processes a single message from a stream.
func (b *StreamEventBus) processMessage(topic string, msg *redis.XMessage) {
	ctx := context.Background()

	// Extract payload
	payloadData, ok := msg.Values["payload"].(string)
	if !ok {
		fmt.Printf("missing payload in message from %s\n", topic)
		return
	}

	// Unmarshal payload
	var payload Event
	if err := jsonx.Unmarshal([]byte(payloadData), &payload); err != nil {
		fmt.Printf("failed to unmarshal payload from %s: %v\n", topic, err)
		return
	}

	// Get handlers for this topic
	b.handlersMu.RLock()
	handlers, exists := b.handlers[topic]
	b.handlersMu.RUnlock()

	if !exists || len(handlers) == 0 {
		return
	}

	// Call handlers. If ANY handler returns an error, skip XACK so the message
	// stays pending and can be redelivered by ClaimPending on a later tick
	// (platform-M3: previously the unconditional XACK permanently dropped events
	// on transient handler failures, violating the documented at-least-once
	// guarantee).
	handlerFailed := false
	for _, handler := range handlers {
		if err := handler(ctx, payload); err != nil {
			fmt.Printf("handler error for topic %s: %v (message %s left pending for retry)\n", topic, err, msg.ID)
			handlerFailed = true
			// Continue processing other handlers so a single slow/buggy handler
			// does not starve the rest; the message will be retried as a unit.
		}
	}

	if handlerFailed {
		return
	}

	// All handlers succeeded — safe to ACK.
	if err := b.redis.XAck(ctx, topic, b.consumerGroup, msg.ID).Err(); err != nil {
		fmt.Printf("failed to ACK message %s from %s: %v\n", msg.ID, topic, err)
	}
}

// ensureGroup ensures the consumer group exists for a stream. It is idempotent:
// BUSYGROUP (group already exists) is treated as success, any other error is
// returned.
func (b *StreamEventBus) ensureGroup(ctx context.Context, stream string) error {
	// Try to create the consumer group with MKSTREAM so the stream is created on
	// first use. Redis replies with BUSYGROUP when the group already exists.
	createErr := b.redis.Do(ctx, "XGROUP", "CREATE", stream, b.consumerGroup, "0", "MKSTREAM").Err()
	if createErr == nil {
		return nil
	}

	// BUSYGROUP → group already exists, nothing to do.
	if strings.Contains(strings.ToLower(createErr.Error()), "busygroup") {
		return nil
	}

	// Any other error during CREATE: confirm whether the group truly exists by
	// listing groups (the XGROUP CREATE can fail for unrelated reasons, e.g. the
	// stream was just trimmed). We must NOT shadow createErr — if the group is
	// genuinely missing we return the original CREATE error so the caller sees
	// the real cause (platform-M6).
	groups, infoErr := b.redis.XInfoGroups(ctx, stream).Result()
	if infoErr != nil {
		return fmt.Errorf("ensureGroup %s: create failed (%w) and XInfoGroups lookup failed: %v",
			stream, createErr, infoErr)
	}
	for _, group := range groups {
		if group.Name == b.consumerGroup {
			return nil
		}
	}
	return fmt.Errorf("ensureGroup %s: group %s not found after create failed: %w",
		stream, b.consumerGroup, createErr)
}

// Close closes the event bus and waits for all consumers to finish.
func (b *StreamEventBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true
	b.cancel()
	b.wg.Wait()

	return nil
}

// StreamStats holds statistics for a stream.
type StreamStats struct {
	Stream          string
	ConsumerGroup   string
	Consumers       []string
	Pending         int64
	LastDeliveredID string
}

// Stats returns statistics for all streams being consumed.
//
// The set of topics is snapshotted under the handlers lock and all Redis IO
// happens after the lock is released, so a slow Redis cannot block
// Subscribe/Unsubscribe on other goroutines.
func (b *StreamEventBus) Stats(ctx context.Context) ([]*StreamStats, error) {
	b.handlersMu.RLock()
	topics := make([]string, 0, len(b.handlers))
	for topic := range b.handlers {
		topics = append(topics, topic)
	}
	b.handlersMu.RUnlock()

	var stats []*StreamStats

	for _, topic := range topics {
		// Get consumer group info
		info, err := b.redis.XInfoGroups(ctx, topic).Result()
		if err != nil {
			continue
		}

		for _, group := range info {
			if group.Name == b.consumerGroup {
				// Get consumers
				consumers, err := b.redis.XInfoConsumers(ctx, topic, b.consumerGroup).Result()
				if err != nil {
					continue
				}

				var consumerNames []string
				for _, consumer := range consumers {
					consumerNames = append(consumerNames, consumer.Name)
				}

				stats = append(stats, &StreamStats{
					Stream:          topic,
					ConsumerGroup:   group.Name,
					Consumers:       consumerNames,
					Pending:         group.Pending,
					LastDeliveredID: group.LastDeliveredID,
				})
			}
		}
	}

	return stats, nil
}

// ClaimPending processes pending (un-ACKed) messages for a stream that were
// left behind by a crashed or slow consumer. It uses XAUTOCLAIM to transfer
// ownership of messages idle for longer than minIdleTime to this consumer and
// re-delivers them to the registered handlers. Returns the number of messages
// reclaimed.
//
// This must only be called for topics that have been Subscribe'd (and thus
// have a consumer group).
func (b *StreamEventBus) ClaimPending(ctx context.Context, topic string, minIdleTime time.Duration) (int, error) {
	const batchSize = "100"
	cursor := "0-0"
	claimed := 0

	for {
		msgs, next, err := b.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   topic,
			Group:    b.consumerGroup,
			Consumer: b.consumerID,
			MinIdle:  minIdleTime,
			Start:    cursor,
			Count:    100,
		}).Result()
		if err != nil && err != redis.Nil {
			return claimed, fmt.Errorf("xautoclaim on %s: %w", topic, err)
		}

		for i := range msgs {
			b.processMessage(topic, &msgs[i])
			claimed++
		}

		cursor = next
		if next == "" || next == "0-0" {
			break
		}
		if len(msgs) == 0 {
			break
		}
		_ = batchSize // batchSize kept for documentation; Count is hardcoded above
	}

	return claimed, nil
}

// Trim trims the stream to the specified maximum length.
func (b *StreamEventBus) Trim(ctx context.Context, topic string, exact bool, maxLen int64) error {
	if exact {
		return b.redis.Do(ctx, "XTRIM", topic, "MAXLEN", maxLen).Err()
	}
	return b.redis.Do(ctx, "XTRIM", topic, "MAXLEN", "~", maxLen).Err()
}

// ReadLast reads the last N messages from a stream without consumer group.
// Useful for debugging or backfilling.
func (b *StreamEventBus) ReadLast(ctx context.Context, topic string, start, stop string) ([]redis.XMessage, error) {
	return b.redis.XRevRange(ctx, topic, start, stop).Result()
}
