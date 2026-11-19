package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
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
	ephemeral     bool
	handlers      map[string][]Handler
	handlersMu    sync.RWMutex
	maxlen        int64
	readTimeout   time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	started       map[string]struct{}
	processingMu  sync.Mutex
	processing    map[string]struct{}
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
		started:       make(map[string]struct{}),
		processing:    make(map[string]struct{}),
	}
}

// NewEphemeralStreamEventBus gives a process-local subscriber an independent,
// expiring group. Its handlers must be safe to replay after a lease lapses.
func NewEphemeralStreamEventBus(redisClient *redis.Client, consumerID string) *StreamEventBus {
	bus := NewStreamEventBus(redisClient, consumerID+"-"+uuid.NewString())
	bus.ephemeral = true
	return bus
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
		Values: map[string]any{
			"payload":   string(data),
			"timestamp": time.Now().UnixNano(),
			"producer":  b.consumerID,
		},
	}).Err()

	if err != nil {
		return fmt.Errorf("publish event to stream %s: %w", topic, err)
	}

	if err := b.redis.Eval(ctx, trimAcknowledgedStreamScript, streamLeaseKeys(topic), b.maxlen).Err(); err != nil {
		metrics.EventStreamFailures.WithLabelValues(topic, "trim_error").Inc()
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
	if _, exists := b.started[topic]; exists {
		return
	}
	b.started[topic] = struct{}{}

	// Ensure consumer group exists
	if err := b.ensureGroup(b.ctx, topic); err != nil {
		// Log error but don't fail - will retry on consume
		fmt.Printf("failed to ensure consumer group for %s: %v\n", topic, err)
	}

	b.wg.Add(2)
	go b.consumeLoop(topic)
	go b.claimLoop(topic)
	if b.ephemeral {
		b.wg.Go(func() { b.renewGroupLease(topic) })
	}
}

const (
	pendingClaimInterval = 10 * time.Second
	pendingClaimIdle     = 30 * time.Second
	ephemeralGroupTTL    = time.Minute
)

func (b *StreamEventBus) renewGroupLease(topic string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
			err := b.ensureGroup(ctx, topic)
			cancel()
			if err != nil && b.ctx.Err() == nil {
				metrics.EventStreamFailures.WithLabelValues(topic, "lease_error").Inc()
			}
		}
	}
}

// claimLoop reclaims messages whose consumer died before ACK. The idle
// threshold is longer than the normal read timeout so a live slow handler is
// not reclaimed prematurely; handlers must remain idempotent.
func (b *StreamEventBus) claimLoop(topic string) {
	defer b.wg.Done()
	ticker := time.NewTicker(pendingClaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(b.ctx, pendingClaimInterval)
			if _, err := b.ClaimPending(ctx, topic, pendingClaimIdle); err != nil {
				fmt.Printf("error reclaiming pending messages from %s: %v\n", topic, err)
			}
			cancel()
		}
	}
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
			if redis.HasErrorPrefix(err, "NOGROUP") && b.ensureGroup(b.ctx, topic) == nil {
				continue
			}
			fmt.Printf("error reading from stream %s: %v\n", topic, err)
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(time.Second):
			}
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
	key := topic + ":" + msg.ID
	b.processingMu.Lock()
	if _, exists := b.processing[key]; exists {
		b.processingMu.Unlock()
		return
	}
	b.processing[key] = struct{}{}
	b.processingMu.Unlock()
	defer func() {
		b.processingMu.Lock()
		delete(b.processing, key)
		b.processingMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	pending, err := b.redis.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: b.consumerGroup, Start: msg.ID, End: msg.ID, Count: 1}).Result()
	if err != nil {
		metrics.EventStreamFailures.WithLabelValues(topic, "pending_error").Inc()
		return
	}
	if len(pending) == 0 {
		return
	}
	if pending[0].RetryCount > 10 {
		if err := b.redis.Eval(ctx, deadLetterStreamScript, []string{topic, topic + ".dlq"}, b.consumerGroup, msg.ID, fmt.Sprint(msg.Values["payload"])).Err(); err != nil {
			metrics.EventStreamFailures.WithLabelValues(topic, "dead_letter_error").Inc()
		} else {
			metrics.EventStreamFailures.WithLabelValues(topic, "dead_letter").Inc()
		}
		return
	}
	// Extract payload
	payloadData, ok := msg.Values["payload"].(string)
	if !ok {
		metrics.EventStreamFailures.WithLabelValues(topic, "malformed_payload").Inc()
		fmt.Printf("missing payload in message from %s\n", topic)
		return
	}

	// Unmarshal payload
	var payload Event
	if err := jsonx.Unmarshal([]byte(payloadData), &payload); err != nil {
		metrics.EventStreamFailures.WithLabelValues(topic, "malformed_payload").Inc()
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
		if err := invokeHandler(ctx, handler, payload); err != nil {
			metrics.EventStreamFailures.WithLabelValues(topic, "handler_error").Inc()
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

func invokeHandler(ctx context.Context, handler Handler, event Event) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("event handler panic: %v", value)
		}
	}()
	return handler(ctx, event)
}

// ensureGroup ensures the consumer group exists for a stream. It is idempotent:
// BUSYGROUP (group already exists) is treated as success, any other error is
// returned.
func (b *StreamEventBus) ensureGroup(ctx context.Context, stream string) error {
	if b.ephemeral {
		return b.redis.Eval(ctx, renewGroupLeaseScript, streamLeaseKeys(stream), b.consumerGroup, int64(ephemeralGroupTTL/time.Second)).Err()
	}
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

	if !b.ephemeral {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var closeErr error
	for topic := range b.started {
		if err := b.redis.Eval(ctx, destroyGroupLeaseScript, streamLeaseKeys(topic), b.consumerGroup).Err(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("destroy ephemeral group for %s: %w", topic, err))
		}
	}
	return closeErr
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

		claimed += len(msgs)
		for i := range msgs {
			b.processMessage(topic, &msgs[i])
		}

		if next == "" || next == "0-0" || next == cursor {
			break
		}
		cursor = next
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

func streamLeaseKeys(topic string) []string {
	return []string{topic, "event-stream:ephemeral-groups:" + topic}
}

const renewGroupLeaseScript = `
local now=redis.call('TIME')
redis.call('ZADD',KEYS[2],'+inf','enabled',tonumber(now[1])+tonumber(ARGV[2]),ARGV[1])
local created=redis.pcall('XGROUP','CREATE',KEYS[1],ARGV[1],'0','MKSTREAM')
if type(created)=='table' and created.err and not string.find(created.err,'BUSYGROUP',1,true) then return created end
return 1
`

const destroyGroupLeaseScript = `
if redis.call('ZSCORE',KEYS[2],ARGV[1]) and redis.call('EXISTS',KEYS[1])~=0 then
 redis.call('XGROUP','DESTROY',KEYS[1],ARGV[1])
end
return redis.call('ZREM',KEYS[2],ARGV[1])
`

// Preserve unread and pending messages for each group with a live subscriber or durable history.
const trimAcknowledgedStreamScript = `
local ephemeral=redis.call('ZSCORE',KEYS[2],'enabled')
if ephemeral and redis.call('EXISTS',KEYS[1])~=0 then
 local now=redis.call('TIME')
 for _,group in ipairs(redis.call('ZRANGEBYSCORE',KEYS[2],'-inf',now[1],'LIMIT',0,100)) do
  redis.call('XGROUP','DESTROY',KEYS[1],group)
  redis.call('ZREM',KEYS[2],group)
 end
end
if redis.call('XLEN',KEYS[1]) <= tonumber(ARGV[1]) then return 0 end
local groups=redis.call('XINFO','GROUPS',KEYS[1])
local floor=nil
local function earlier(a,b)
 local am,as=string.match(a,'(%d+)%-(%d+)')
 local bm,bs=string.match(b,'(%d+)%-(%d+)')
 return tonumber(am)<tonumber(bm) or (tonumber(am)==tonumber(bm) and tonumber(as)<tonumber(bs))
end
for _,group in ipairs(groups) do
 local name,delivered
 for i=1,#group,2 do
  if group[i]=='name' then name=group[i+1] end
  if group[i]=='last-delivered-id' then delivered=group[i+1] end
 end
 local pending=redis.call('XPENDING',KEYS[1],name)
 if pending[1]>0 and earlier(pending[2],delivered) then delivered=pending[2] end
 if not floor or earlier(delivered,floor) then floor=delivered end
end
if floor then return redis.call('XTRIM',KEYS[1],'MINID',floor) end
if ephemeral then return redis.call('XTRIM',KEYS[1],'MAXLEN',ARGV[1]) end
return 0
`
const deadLetterStreamScript = `
if #redis.call('XPENDING',KEYS[1],ARGV[1],ARGV[2],ARGV[2],1)==0 then return 0 end
redis.call('XADD',KEYS[2],'*','payload',ARGV[3],'source_id',ARGV[2],'consumer_group',ARGV[1],'reason','delivery attempts exceeded')
return redis.call('XACK',KEYS[1],ARGV[1],ARGV[2])
`
