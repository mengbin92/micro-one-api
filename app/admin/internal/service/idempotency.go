package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// admin 层资金写路径的请求级幂等键处理。
//
// 协议与 relay 幂等中间件一致(设计文档 docs/design/v0.18-idempotency-decision.md
// §4.3):客户端在写操作请求头携带 `Idempotency-Key`,admin server 读头后透传
// 到 service 层,service 再传给 billing RPC 的 request_id,由 billing 的
// ledger dedupe key 唯一索引做 DB 层去重。

// ErrInvalidIdempotencyKey 表示 Idempotency-Key 头不合法(超长或含不可见字符)。
var ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")

const (
	// maxIdempotencyKeyLen 是客户端幂等键的最大长度(billing 侧
	// ledger_dedupe_key 列宽预算内,见 biz.ledgerDedupeKeyFor)。
	maxIdempotencyKeyLen = 100
)

// normalizeRequestID 规范化客户端幂等键:
//   - 空键(旧客户端未传 Idempotency-Key)→ 生成 `auto:{hex}`,保证 billing 侧
//     扣款 ledger 不撞 legacy 键(修复同 group 购买冲突 bug),但**不提供幂等**
//     (每次调用键不同),兼容旧客户端零改造;
//   - 非空键 → 校验长度/字符集后原样透传,幂等生效。
func normalizeRequestID(requestID string) (string, error) {
	if requestID == "" {
		return "auto:" + randomHex(16), nil
	}
	if len(requestID) > maxIdempotencyKeyLen {
		return "", ErrInvalidIdempotencyKey
	}
	for i := 0; i < len(requestID); i++ {
		c := requestID[i]
		if c < 0x21 || c > 0x7e {
			return "", ErrInvalidIdempotencyKey
		}
	}
	return requestID, nil
}

// randomHex returns n random bytes hex-encoded. crypto/rand.Read only fails
// when the OS entropy source is unavailable; a deterministic fallback would
// collapse auto keys and re-introduce the duplicate-purchase collision this
// package guards against — fail loudly instead (review 2026-08-10).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
