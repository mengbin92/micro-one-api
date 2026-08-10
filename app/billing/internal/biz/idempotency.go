package biz

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

// 资金写路径的 ledger 幂等键(action 前缀)。
//
// 设计文档 docs/design/v0.18-idempotency-decision.md §4.2:
// 扣款/充值 ledger 显式携带 (user_id, request_id) 派生的 dedupe key,复用
// billing_ledgers.ledger_dedupe_key 的全局唯一索引作为请求级幂等闸门。
// 键格式 `{action}:{user_id}:{request_id}` 与 legacy 格式
// (`{reference_id}:{type}:legacy`,不含 user_id)天然区分,互不冲突。
const (
	// DedupeActionPurchase 是购买/升级扣款 ledger 的幂等键前缀
	// (type=subscription)。
	DedupeActionPurchase = "purchase"
	// DedupeActionTopup 是充值 ledger 的幂等键前缀 (type=recharge)。
	DedupeActionTopup = "topup"
)

// ErrDuplicateRequest 表示同 (user_id, request_id) 的资金写请求重复提交。
// 由 DB 唯一索引冲突(或事务内预检命中)触发,调用方应将其映射为 409。
var ErrDuplicateRequest = errors.New("duplicate request")

// maxDedupeUserIDLen 限制 user_id 在幂等键中的前缀长度,保证键总长不超过
// billing_ledgers.ledger_dedupe_key 列宽(VARCHAR(160)):
//
//	{action}(≤9) + ":" + {user_id 截断 48} + ":" + {request_id ≤ 100}
//	= 9 + 1 + 48 + 1 + 100 = 159 ≤ 160
const maxDedupeUserIDLen = 48

// ledgerDedupeKeyFor 生成资金 ledger 的显式幂等键。
// requestID 为空(客户端未传 Idempotency-Key)时回退为每次调用不同的
// `auto:{hex}`——永不撞 legacy 键(修复 legacy 键不含 user_id 导致的同
// group 购买冲突 bug),但不提供幂等(无键 = 无幂等保证,兼容旧客户端)。
func ledgerDedupeKeyFor(action, userID, requestID string) string {
	uid := userID
	if len(uid) > maxDedupeUserIDLen {
		uid = uid[:maxDedupeUserIDLen]
	}
	if requestID == "" {
		requestID = "auto:" + randomHex(16)
	}
	return action + ":" + uid + ":" + requestID
}

// randomHex returns n random bytes hex-encoded (2n chars). crypto/rand.Read
// only fails when the OS entropy source is unavailable, at which point the
// process cannot operate correctly anyway. A deterministic fallback would
// collapse every auto key onto one dedupe key and silently re-introduce the
// duplicate-purchase collision this package exists to prevent — fail loudly
// instead (review 2026-08-10).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// isDuplicateKeyError 识别三驱动(MySQL 1062 / Postgres 23505 / SQLite)的
// 唯一约束冲突。billing_ledgers.ledger_dedupe_key 的唯一索引冲突是请求级
// 幂等的最终闸门;识别后映射为 ErrDuplicateRequest 而非透传驱动错误。
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint")
}
