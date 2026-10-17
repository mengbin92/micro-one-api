package data

import (
	"math"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/security/serviceidentity"
	"strconv"
	"strings"
)

func safeTracePhase(name string) string {
	if requesttrace.ValidTimingPhase(name) || name == "relay.request" {
		return name
	}
	switch name {
	case "network.connection", "network.dns", "network.connect", "network.tls", "db.query", "db.insert", "db.update", "db.delete", "db.raw", "db.row", "db.transaction", "cache.redis", "cache.pipeline", "settlement.worker", "execution.outbox.worker", "log.flush", "log.batch_item", "health.worker":
		return name
	}
	if _, ok := serviceidentity.Lookup(name); ok {
		return name
	}
	return "unclassified"
}
func safeTraceService(name string) string {
	if name == "" || len(name) > 64 {
		return "unknown"
	}
	for _, c := range name {
		if c != '-' && c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return "unknown"
		}
	}
	return name
}
func safeTraceAttribute(key, value string) (string, bool) {
	switch key {
	case "rpc.system":
		return value, value == "grpc"
	case "rpc.method":
		_, ok := serviceidentity.Lookup(value)
		return value, ok
	case "rpc.status":
		switch value {
		case "OK", "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented", "Internal", "Unavailable", "DataLoss", "Unauthenticated":
			return value, true
		}
	case "network.connection.reused", "network.connection.idle", "cache.hit", "batch.shared", "telemetry.carrier.valid":
		return value, value == "true" || value == "false"
	case "http.response.status_code":
		n, err := strconv.Atoi(value)
		return value, err == nil && n >= 100 && n <= 599
	case "queue.wait_ms":
		n, err := strconv.ParseFloat(value, 64)
		return value, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "db.operation.name":
		for _, name := range strings.Fields("query insert update delete raw row get set setex psetex del unlink exists eval evalsha hget hset hgetall expire pexpire ttl pttl incr incrby mget mset zadd zrange zrem sadd smembers srem ping script") {
			if name == value {
				return value, true
			}
		}
		return "other", true
	case "upstream.protocol":
		switch value {
		case "openai_chat", "responses", "anthropic_messages", "gemini", "embeddings", "other":
			return value, true
		}
	}
	return "", false
}
