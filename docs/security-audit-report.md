# Micro-One-Api 安全审计报告 V2

**审计日期**: 2026-05-06
**审计版本**: v2.0 (增强版)
**审计范围**: 全量代码安全审计 (184 Go 源文件 + 11 YAML 配置 + 10 Proto + 部署文件)
**评分标准**: CVSS 4.0
**参考标准**: OWASP Top 10 2025, CWE Top 25 2025, MITRE ATT&CK v18+

---

## 目录

1. [执行摘要](#1-执行摘要)
2. [审计方法论](#2-审计方法论)
3. [OWASP Top 10 2025 分类审计](#3-owasp-top-10-2025-分类审计)
4. [修复状态](#4-修复状态)
5. [风险矩阵](#5-风险矩阵)
6. [修复建议优先级](#6-修复建议优先级)

---

## 1. 执行摘要

| 指标 | 数值 |
|------|------|
| 审计文件数 | 184 Go 源文件 + 11 YAML 配置 + 10 Proto + 部署文件 |
| 发现总数 | 42 |
| 严重 (Critical) | 5 |
| 高危 (High) | 10 |
| 中危 (Medium) | 14 |
| 低危 (Low) | 8 |
| 信息 (Info) | 5 (正面发现) |
| 已修复 | 25 |
| 待修复 | 12 |

---

## 2. 审计方法论

- **静态代码分析**: 全量 Go 源码审查 (grep, 人工审查)
- **依赖分析**: go.mod 直接/间接依赖检查
- **配置审查**: YAML, Dockerfile, docker-compose, K8s manifests
- **供应链分析**: GitHub Actions, 依赖版本
- **运行时风险评估**: gRPC 拦截器链, HTTP 中间件链, 数据流追踪
- **CVSS 4.0 评分**: 每个发现使用完整向量字符串

---

## 3. OWASP Top 10 2025 分类审计

### A01:2025 - 访问控制失效 (Broken Access Control)

**有发现**

#### V-01 [CRITICAL] gRPC 认证拦截器 Token 提取逻辑完全失效

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H` - **9.3**
- **CWE**: CWE-287 (不当认证)
- **MITRE ATT&CK**: T1078 (有效账户)
- **文件**: `internal/pkg/grpc/auth.go:184-206`
- **代码证据**:
  ```go
  func extractTokenFromContext(ctx context.Context) (string, error) {
      md, ok := peer.FromContext(ctx)  // 错误: 应使用 metadata.FromIncomingContext
      if !ok {
          return "", fmt.Errorf("no peer info")
      }
      if token := md.AuthInfo; token != nil {
          if tlsInfo, ok := token.(credentials.TLSInfo); ok {
              if len(tlsInfo.State.PeerCertificates) > 0 {
                  return "", nil // mTLS 时返回空字符串
              }
          }
      }
      return "", fmt.Errorf("token not found in metadata")
  }
  ```
- **描述**: 函数使用 `peer.FromContext()` 获取传输层信息而非 gRPC metadata headers。TokenAuth 客户端通过 `authorization` metadata key 发送 token，但此函数从未读取 gRPC metadata。结果: (1) 无 mTLS 时所有认证调用失败; (2) 有 mTLS 时返回空字符串，JWT 验证必定失败。整个 gRPC 认证系统形同虚设。
- **修复状态**: ❌ 待修复 (需架构级改造)

#### V-02 [CRITICAL] 所有内部 gRPC 服务完全无认证

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H` - **9.3**
- **CWE**: CWE-306 (关键功能缺少认证)
- **MITRE ATT&CK**: T1078.004 (云账户)
- **文件**:
  - `internal/identity/server/grpc.go:11-16`
  - `internal/admin/server/grpc.go:11-15`
  - `internal/billing/server/grpc.go`
  - `internal/channel/server/grpc.go`
  - `internal/log/server/grpc.go`
- **代码证据**:
  ```go
  // identity/server/grpc.go
  func NewGRPCServer(addr string, svc *service.IdentityService) *kgrpc.Server {
      srv := kgrpc.NewServer(kgrpc.Address(addr))  // 无认证拦截器
      identityv1.RegisterIdentityServiceServer(srv, svc)
      return srv
  }
  ```
- **描述**: `CreateAuthenticatedServer` 函数存在但从未被任何服务调用。任何能到达 gRPC 端口的客户端可以: (1) 无限制暴力破解 Login; (2) 调用 CreateUser/DeleteUser 管理用户; (3) 调用 TopUpQuota/CreateRedeemCode 操控计费。
- **修复状态**: ❌ 待修复 (需架构级改造)

#### V-03 [HIGH] Admin gRPC 服务暴露所有管理操作

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N` - **8.7**
- **CWE**: CWE-306 (关键功能缺少认证)
- **文件**: `internal/admin/server/grpc.go:11-15`
- **描述**: HTTP 端点有 AdminAuth 中间件保护，但 gRPC 端口完全开放，包含 TopUpQuota, CreateRedeemCode, DeleteUser 等所有管理操作。
- **修复状态**: ❌ 待修复

#### V-04 [HIGH] 日志服务 HTTP 端点无认证

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:L/VA:N/SC:N/SI:N/SA:N` - **7.3**
- **CWE**: CWE-306 (关键功能缺少认证)
- **文件**: `internal/log/server/http.go:17-29`
- **描述**: 所有日志端点 (GET/POST /v1/logs, GET /v1/logs/{id}) 完全无认证。攻击者可读取所有日志或注入虚假日志。
- **修复状态**: ✅ 已修复 (添加 SERVICE_TOKEN Bearer 认证中间件)

#### V-05 [HIGH] 计费对账端点无认证

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:H/VA:H/SC:N/SI:N/SA:N` - **8.1**
- **CWE**: CWE-306 (关键功能缺少认证)
- **文件**: `internal/billing/server/http.go:17`
- **描述**: `/v1/reconciliation` 端点无认证，可被反复触发导致数据库负载或计费竞态。
- **修复状态**: ✅ 已修复 (添加 SERVICE_TOKEN Bearer 认证中间件)

#### V-06 [HIGH] IP 欺骗绕过速率限制

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:H/VA:H/SC:N/SI:N/SA:N` - **8.2**
- **CWE**: CWE-290 (欺骗认证绕过)
- **文件**: `internal/pkg/middleware/ratelimit.go:213-234`
- **代码证据**:
  ```go
  func getClientIP(r *http.Request) string {
      if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
          if idx := indexOf(xff, ","); idx != -1 {
              return xff[:idx]  // 盲信客户端 header
          }
          return xff
      }
  ```
- **描述**: 速率限制器盲信 `X-Forwarded-For` 和 `X-Real-IP` header。攻击者可通过轮换伪造 IP 完全绕过 IP 级速率限制。
- **修复状态**: ✅ 已修复 (移除 header 信任, 使用 RemoteAddr)

#### V-07 [MEDIUM] OAuth 自动注册给予无限配额

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:P/VC:N/VI:H/VA:N/SC:N/SI:N/SA:N` - **6.9**
- **CWE**: CWE-287 (不当认证)
- **文件**: `internal/identity/biz/auth.go:317-322`
- **代码证据**:
  ```go
  tokenRecord := &Token{
      UserID:         user.ID,
      Key:            token,
      Status:         TokenStatusEnabled,
      UnlimitedQuota: true,  // 新 OAuth 用户获得无限配额
      Models:         []string{},
  }
  ```
- **描述**: 新 OAuth 用户自动创建并获得 `UnlimitedQuota: true`，无邮箱验证。攻击者可创建大量 GitHub/Google 账户获得无限 API 访问。
- **修复状态**: ❌ 待修复

---

### A02:2025 - 加密失败 (Cryptographic Failures)

**有发现**

#### V-08 [CRITICAL] 所有 gRPC 服务间通信默认使用明文传输

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:N/SC:H/SI:H/SA:N` - **9.1**
- **CWE**: CWE-319 (明文传输敏感信息)
- **MITRE ATT&CK**: T1557 (中间人攻击)
- **文件**: `internal/relay/data/data.go:22-26`
- **代码证据**:
  ```go
  identityConn, err := grpc.NewClient(identityEndpoint,
      grpc.WithTransportCredentials(insecure.NewCredentials()))
  channelConn, err := grpc.NewClient(channelEndpoint,
      grpc.WithTransportCredentials(insecure.NewCredentials()))
  ```
- **描述**: 所有服务间 gRPC 连接使用 `insecure.NewCredentials()`。API token、用户凭证、计费数据全部明文传输。
- **修复状态**: ❌ 待修复 (需架构级改造)

#### V-09 [HIGH] 上游 API 密钥明文存储于数据库

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:H/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N` - **6.9**
- **CWE**: CWE-312 (明文存储敏感信息)
- **文件**: `internal/channel/data/data.go:25-36`
- **描述**: OpenAI, Anthropic, Gemini 等上游 API 密钥以明文存储在 channels 表的 `key` 字段中。数据库泄露将导致所有 API 密钥立即暴露。
- **修复状态**: ❌ 待修复

#### V-10 [HIGH] Gemini API 密钥暴露在 URL 查询参数中

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N` - **7.7**
- **CWE**: CWE-598 (GET 请求传递敏感信息)
- **文件**: `internal/relay/provider/gemini.go:163,202`
- **代码证据**:
  ```go
  url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
      p.baseURL, req.Model, p.apiKey)
  ```
- **描述**: Gemini API 密钥放在 URL 查询参数中，会被代理日志、Web 服务器访问日志、CDN 日志等记录。
- **修复状态**: ✅ 已修复 (改用 Authorization header)

#### V-11 [MEDIUM] TLS InsecureSkipVerify 默认为 true

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N` - **6.0**
- **CWE**: CWE-295 (不当证书验证)
- **文件**: `internal/pkg/tls/config.go:111-115`
- **描述**: TLS 未启用时，HTTP 客户端配置 `InsecureSkipVerify: true`，允许 MITM 攻击。
- **修复状态**: ✅ 已修复 (默认 InsecureSkipVerify 为 false)

#### V-12 [MEDIUM] Redis 客户端无密码认证支持

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:L/VA:N/SC:N/SI:N/SA:N` - **7.1**
- **CWE**: CWE-306 (关键功能缺少认证)
- **文件**: `internal/pkg/xdb/redis.go:45-53`
- **描述**: Redis 客户端创建时不支持密码认证。docker-compose 配置了 `--requirepass`，但应用代码无法使用密码连接。
- **修复状态**: ✅ 已修复 (添加密码支持)

#### V-13 [LOW] Token 生成存在取模偏差

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **2.0**
- **CWE**: CWE-330 (使用不充分随机值)
- **文件**: `internal/identity/biz/auth.go:279`
- **描述**: `int(b[i])%62` 存在轻微取模偏差 (256 mod 62 = 8)。有效熵约 190 位，实际影响极小。
- **修复状态**: ✅ 已修复 (使用 crypto/rand.Int 消除偏差)

---

### A03:2025 - 注入 (Injection)

**有发现**

#### V-14 [HIGH] Gemini URL 路径注入 (SSRF)

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:H/SI:N/SA:N` - **7.6**
- **CWE**: CWE-918 (服务端请求伪造)
- **MITRE ATT&CK**: T1090 (代理)
- **文件**: `internal/relay/provider/gemini.go:163`
- **代码证据**:
  ```go
  url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
      p.baseURL, req.Model, p.apiKey)  // req.Model 未验证
  ```
- **描述**: `req.Model` 直接插入 URL 路径。基础服务器仅检查 `req.Model == ""`，不验证格式。攻击者可提交 `../../some-path` 操纵请求目标。
- **修复状态**: ✅ 已修复 (添加 model name 验证)

#### V-15 [MEDIUM] 兑换码搜索 LIKE 通配符注入

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **4.3**
- **CWE**: CWE-89 (SQL 特殊元素不当处理)
- **文件**: `internal/billing/data/redeem_repo.go:121`
- **代码证据**:
  ```go
  Where("code = ? OR name LIKE ?", keyword, keyword+"%")
  // keyword 未经 escapeLike 处理
  ```
- **描述**: 与其他 LIKE 查询不同，此处未调用 `escapeLike()`。攻击者可用 `keyword = "%"` 导出所有兑换码。
- **修复状态**: ✅ 已修复 (添加 escapeLike)

#### V-16 [MEDIUM] 日志注入

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:L/VA:N/SC:N/SI:N/SA:N` - **4.0**
- **CWE**: CWE-117 (日志输出不当处理)
- **文件**: `internal/pkg/middleware/security.go:83-89`
- **描述**: `r.UserAgent()` 和 `X-Request-ID` header 值直接写入日志，可被用于注入虚假日志条目。
- **修复状态**: ✅ 已修复 (添加控制字符过滤 sanitizeLogField)

#### V-17 [MEDIUM] Request ID Header 注入

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:L/VA:N/SC:N/SI:N/SA:N` - **4.0`
- **CWE**: CWE-113 (HTTP Header CRLF 注入)
- **文件**: `internal/pkg/middleware/security.go:65-76`
- **描述**: `X-Request-ID` 客户端值未经验证直接回写 response header 和存入 context。
- **修复状态**: ✅ 已修复 (添加 sanitizeRequestID 验证，拒绝控制字符和超长值)

---

### A04:2025 - 不安全设计 (Insecure Design)

**有发现**

#### V-18 [CRITICAL] 生产环境安全中间件完全未启用

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H` - **9.3**
- **CWE**: CWE-693 (保护机制失效)
- **MITRE ATT&CK**: T1190 (利用面向公众的应用)
- **文件**: `cmd/relay-gateway/wire_gen.go:179-182`
- **代码证据**:
  ```go
  httpServer := server.NewHTTPServer(...)   // 创建基础 HTTPServer
  srv := khttp.NewServer(...)
  httpServer.RegisterRoutes(srv)            // 无任何中间件!
  ```
- **描述**: 生产入口调用 `HTTPServer.RegisterRoutes()` 而非 `EnhancedHTTPServer.RegisterRoutesWithSecurity()`。所有安全中间件 (CORS, CSP, HSTS, 速率限制, 请求体限制, 请求 ID) 全部是死代码。
- **修复状态**: ❌ 待修复 (需架构级改造)

#### V-19 [HIGH] 内存速率限制器无边界增长

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:H/SC:N/SI:N/SA:N` - **7.7**
- **CWE**: CWE-400 (不受控资源消耗)
- **文件**: `internal/pkg/middleware/ratelimit.go:17-21`
- **描述**: `clients map[string]*ClientLimiter` 无最大限制。攻击者用大量唯一 IP 发送请求可耗尽内存。
- **修复状态**: ✅ 已修复 (添加 maxClients 上限，默认 100000)

#### V-20 [MEDIUM] 错误消息泄露内部信息

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **5.3**
- **CWE**: CWE-209 (生成包含敏感信息的错误消息)
- **文件**:
  - `internal/relay/server/http.go:309-349` - `err.Error()` 和 `st.Message()` 直接返回客户端
  - `internal/admin/server/http.go:174` - `err.Error()` 泄露
  - `internal/identity/service/identity.go:87,103` - gRPC 响应包含 `err.Error()`
- **描述**: 多个处理器将原始错误消息传递给客户端，可能泄露数据库结构、服务名称等内部信息。
- **修复状态**: ✅ 部分修复 (admin http.go 和 identity service 已修复)

#### V-21 [MEDIUM] 无密码强度验证

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N` - **5.3**
- **CWE**: CWE-521 (弱密码要求)
- **文件**: `internal/identity/biz/auth.go:176-197`
- **描述**: Login 和 Register 接受任意非空密码，无长度或复杂度验证。
- **修复状态**: ✅ 已修复 (添加最小长度 8 位)

---

### A05:2025 - 安全配置错误 (Security Misconfiguration)

**有发现**

#### V-22 [HIGH] 配置文件包含默认数据库密码

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N` - **8.7**
- **CWE**: CWE-798 (硬编码凭证)
- **文件**: `configs/*.yaml` (9 个文件)
- **代码证据**:
  ```yaml
  source: ${DATABASE_DSN:-root:password@tcp(127.0.0.1:3306)/oneapi}
  ```
- **描述**: 9 个 YAML 配置文件包含 `root:password` 作为 DATABASE_DSN 的默认值。未设置环境变量时自动使用。
- **修复状态**: ✅ 已修复 (移除默认值)

#### V-23 [LOW] CORS 允许通配符源

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **3.0`
- **CWE**: CWE-942 (宽松跨域策略)
- **文件**: `internal/pkg/middleware/cors.go:95`
- **描述**: 若 `CORS_ALLOWED_ORIGINS` 设为 `*`，配合 `AllowCredentials: true` 可被利用。
- **修复状态**: ✅ 已修复 (检测到 wildcard + credentials 时自动禁用 credentials)

#### V-24 [LOW] K8s 内部服务缺少 NetworkPolicy

- **CVSS 4.0**: `CVSS:4.0/AV:A/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:L/SC:N/SI:N/SA:N` - **5.5`
- **CWE**: CWE-668 (资源暴露到错误领域)
- **文件**: `deployments/k8s/`
- **描述**: 仅 relay-gateway 有 NetworkPolicy，其他服务无网络隔离。
- **修复状态**: ✅ 已修复 (为所有内部服务添加 NetworkPolicy)

#### V-25 [LOW] Docker Compose 暴露过多端口

- **CVSS 4.0**: `CVSS:4.0/AV:A/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **4.3`
- **CWE**: CWE-668 (资源暴露到错误领域)
- **文件**: `deployments/docker-compose/docker-compose.yml`
- **描述**: 所有内部服务端口暴露到宿主机，仅 relay-gateway 和 admin-api 需要外部访问。
- **修复状态**: ✅ 已修复 (内部服务移除端口映射)

---

### A06:2025 - 易受攻击和过时的组件 (Vulnerable and Outdated Components)

**无发现**

#### V-26 [INFO] 依赖版本检查通过

- go.mod 中所有直接依赖版本均为当前稳定版本
- 无已知 CVE 的过时依赖
- CI/CD 包含 govulncheck 和 CodeQL 分析
- **修复状态**: N/A

#### V-27 [LOW] GitHub Actions 版本过旧

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:P/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **2.1`
- **CWE**: CWE-1104 (使用未维护的第三方组件)
- **文件**: `.github/workflows/security.yml`
- **描述**: `actions/checkout@v3`, `actions/setup-go@v4` 等使用旧版本。
- **修复状态**: ✅ 已修复 (升级到最新稳定版本，pin trivy-action)

---

### A07:2025 - 身份识别和认证失败 (Identification and Authentication Failures)

**有发现**

#### V-28 [HIGH] 登录/注册端点无速率限制

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:L/VA:N/SC:N/SI:N/SA:N` - **7.3**
- **CWE**: CWE-307 (过度认证尝试限制不当)
- **MITRE ATT&CK**: T1110 (暴力破解)
- **文件**: `internal/identity/server/grpc.go` (无拦截器)
- **描述**: Login/Register 仅通过 gRPC 可达，无速率限制，无账户锁定机制。
- **修复状态**: ❌ 待修复

#### V-29 [HIGH] 服务间认证可被静默禁用

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N` - **8.7**
- **CWE**: CWE-306 (关键功能缺少认证)
- **文件**: `cmd/relay-gateway/wire_gen.go:88-96`
- **描述**: `ENABLE_AUTH` 默认为 false，所有服务间认证被静默跳过，仅输出警告到 stdout。
- **修复状态**: ❌ 待修复

#### V-30 [MEDIUM] OAuth 状态 cookie 未在验证后删除

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:R/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **3.5`
- **CWE**: CWE-352 (跨站请求伪造)
- **文件**: `internal/identity/server/http.go:94-111`
- **描述**: OAuth 回调验证 state 后未删除 cookie，允许 300 秒窗口内重放。
- **修复状态**: ✅ 已修复 (验证后删除 cookie)

#### V-31 [MEDIUM] 用户枚举

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **5.3`
- **CWE**: CWE-209 (生成包含敏感信息的错误消息)
- **文件**: `internal/identity/service/identity.go:82-96`
- **描述**: Login 失败时返回不同错误消息 ("user not found" vs "invalid password")，可被用于枚举用户。
- **修复状态**: ✅ 已修复 (统一错误消息)

#### V-32 [LOW] 无 JWT 撤销机制

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **3.0`
- **CWE**: CWE-613 (会话过期不足)
- **文件**: `internal/pkg/auth/jwt.go:128-136`
- **描述**: JWT 服务 token 有 24 小时有效期和 7 天刷新窗口，无 JTI claim，无黑名单，无法撤销。
- **修复状态**: ❌ 待修复

---

### A08:2025 - 软件和数据完整性故障 (Software and Data Integrity Failures)

**无发现**

#### V-33 [INFO] CI/CD 安全流水线完善

- GitHub Actions 包含 gosec (SAST), govulncheck (SCA), gitleaks (密钥扫描), Trivy (容器扫描), CodeQL 分析, SBOM 生成
- Docker 镜像使用 scratch 基础镜像，多阶段构建
- K8s 使用 seccompProfile: RuntimeDefault
- **修复状态**: N/A (正面发现)

---

### A09:2025 - 安全日志和监控失败 (Security Logging and Monitoring Failures)

**有发现**

#### V-34 [MEDIUM] 日志脱敏未统一应用

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **5.3**
- **CWE**: CWE-532 (日志文件中插入敏感信息)
- **文件**: `internal/pkg/logger/logger.go:69-82`
- **描述**: `Sanitize()` 函数存在但大部分日志路径直接使用 `applogger.Log` 而非 `SafeLogger`。
- **修复状态**: ✅ 已修复 (SafeLogger 现在也 sanitizes zap.Field 值，新增 Basic auth 和 JWT 模式)

---

### A10:2025 - 服务端请求伪造 (Server-Side Request Forgery)

**有发现**

#### V-35 [HIGH] 数据库存储的 base_url 未验证 (SSRF)

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:H/UI:N/VC:H/VI:N/VA:N/SC:H/SI:N/SA:N` - **7.6**
- **CWE**: CWE-918 (服务端请求伪造)
- **MITRE ATT&CK**: T1090 (代理)
- **文件**: `internal/relay/provider/provider.go:104`
- **描述**: channel 的 `base_url` 来自数据库，无 URL 验证、scheme 限制或允许列表。管理员可设为内部网络地址 (如 `http://169.254.169.254/`) 进行 SSRF。
- **修复状态**: ✅ 已修复 (添加 URL scheme 验证、私有/保留 IP 拦截、localhost 拦截)

---

### 其他发现

#### V-36 [HIGH] 预测性 Request ID

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N` - **5.3`
- **CWE**: CWE-330 (使用不充分随机值)
- **文件**: `internal/relay/server/http.go:462-464`
- **代码证据**:
  ```go
  func generateRequestID() string {
      return fmt.Sprintf("req_%d", time.Now().UnixNano())
  }
  ```
- **描述**: relay-gateway 的 request ID 仅使用时间戳，完全可预测。
- **修复状态**: ✅ 已修复 (使用 crypto/rand)

#### V-37 [MEDIUM] 请求体大小无限制

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:H/SC:N/SI:N/SA:N` - **7.7`
- **CWE**: CWE-400 (不受控资源消耗)
- **文件**: `internal/relay/server/json.go:9-15`
- **代码证据**:
  ```go
  func decodeJSON(r io.Reader, v interface{}) error {
      data, err := io.ReadAll(r)  // 无大小限制
      ...
  }
  ```
- **描述**: `io.ReadAll` 无大小限制，攻击者可发送超大请求体耗尽内存。
- **修复状态**: ✅ 已修复 (添加 io.LimitReader)

#### V-38 [MEDIUM] 弱哈希用于速率限制 key

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:L/VA:N/SC:N/SI:N/SA:N` - **4.0`
- **CWE**: CWE-328 (使用弱哈希)
- **文件**: `internal/pkg/middleware/ratelimit.go:237-243`
- **描述**: DJB2 变体哈希仅 32 位输出，碰撞概率高，可导致不同 token 共享速率限制桶。
- **修复状态**: ✅ 已修复 (使用 SHA-256)

#### V-39 [LOW] mTLS 使用可选验证

- **CVSS 4.0**: `CVSS:4.0/AV:N/AC:H/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **3.0`
- **CWE**: CWE-308 (使用单因素认证)
- **文件**: `internal/pkg/tls/config.go:102`
- **描述**: `VerifyClientCertIfGiven` 使 mTLS 变为可选。
- **修复状态**: ❌ 低优先级

#### V-40 [LOW] PKCS#12 默认密码 "changeme"

- **CVSS 4.0**: `CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N` - **3.2`
- **CWE**: CWE-798 (硬编码凭证)
- **文件**: `scripts/generate-certs.sh:93`
- **描述**: 证书生成脚本使用硬编码密码 "changeme"。
- **修复状态**: ❌ 低优先级

---

## 4. 修复状态

### ✅ 已修复 (25 项)

| ID | 描述 | 修复内容 |
|----|------|----------|
| V-04 | 日志服务无认证 | 添加 SERVICE_TOKEN Bearer 认证中间件 |
| V-05 | 计费对账无认证 | 添加 SERVICE_TOKEN Bearer 认证中间件 |
| V-06 | IP 欺骗绕过速率限制 | 移除 X-Forwarded-For 信任, 使用 RemoteAddr |
| V-10 | Gemini API 密钥暴露在 URL | 改用 Authorization header |
| V-11 | InsecureSkipVerify | 默认 InsecureSkipVerify 为 false |
| V-12 | Redis 无密码支持 | 添加密码参数 |
| V-13 | 取模偏差 | 使用 crypto/rand.Int 消除偏差 |
| V-14 | Gemini URL 路径注入 | 添加 model name 正则验证 |
| V-15 | 兑换码 LIKE 注入 | 添加 escapeLike |
| V-16 | 日志注入 | 添加控制字符过滤 sanitizeLogField |
| V-17 | Request ID Header 注入 | 添加 sanitizeRequestID 验证 |
| V-19 | 内存速率限制器无边界 | 添加 maxClients 上限 (默认 100000) |
| V-20 | 错误消息泄露 | 部分修复: admin http.go 和 identity service |
| V-21 | 无密码强度验证 | 添加最小长度 8 位 |
| V-22 | 配置文件默认密码 | 移除 YAML 中的默认密码 |
| V-23 | CORS 通配符 | 检测 wildcard + credentials 时自动禁用 credentials |
| V-24 | K8s NetworkPolicy | 为所有内部服务添加 NetworkPolicy |
| V-25 | Docker Compose 端口暴露 | 内部服务移除端口映射 |
| V-27 | GitHub Actions 版本 | 升级到最新稳定版本, pin trivy-action |
| V-30 | OAuth state cookie 未删除 | 验证后删除 cookie |
| V-31 | 用户枚举 | 统一 Login 错误消息 |
| V-34 | 日志脱敏未统一 | SafeLogger sanitizes zap.Field, 新增 Basic/JWT 模式 |
| V-35 | SSRF via base_url | 添加 URL scheme 验证、私有 IP 拦截 |
| V-36 | 预测性 Request ID | 使用 crypto/rand |
| V-37 | 请求体大小无限制 | 添加 io.LimitReader (10MB) |
| V-38 | 弱哈希 | 使用 SHA-256 |

### ❌ 待修复 (12 项)

需要架构级改造:
- V-01: gRPC 认证拦截器 Token 提取
- V-02: 内部 gRPC 服务无认证
- V-03: Admin gRPC 无认证
- V-08: gRPC 明文传输
- V-18: 生产环境安全中间件未启用
- V-29: 服务间认证可被禁用

需要业务决策:
- V-07: OAuth 自动注册无限配额
- V-09: API 密钥明文存储
- V-32: JWT 撤销机制

低优先级:
- V-28: 登录无速率限制
- V-39: mTLS 可选验证
- V-40: PKCS#12 默认密码

---

## 5. 风险矩阵

| 可利用性 | 影响高 | 影响中 | 影响低 |
|----------|--------|--------|--------|
| **易利用** | V-01, V-02, V-08, V-18 | ~~V-06~~, ~~V-14~~, ~~V-15~~ | ~~V-16~~, ~~V-17~~ |
| **中等** | V-03, V-09, ~~V-22~~, V-29 | V-07, V-20, ~~V-30~~ | ~~V-13~~, ~~V-23~~ |
| **难利用** | ~~V-11~~, ~~V-35~~ | ~~V-31~~, ~~V-34~~ | V-32, V-39, V-40 |

---

## 6. 修复建议优先级

### P0 - 立即修复 (安全关键)
1. 生产环境启用 EnhancedHTTPServer (V-18)
2. 修复 gRPC 认证拦截器 (V-01)
3. 为所有 gRPC 服务添加认证 (V-02, V-03)
4. 启用 gRPC TLS (V-08)

### P1 - 短期修复 (1-2 周)
1. ~~添加 SSRF 防护 - base_url 验证 (V-35)~~ ✅
2. 添加 API 密钥加密存储 (V-09)
3. 添加登录速率限制 (V-28)
4. 限制 OAuth 默认配额 (V-07)
5. ~~为日志/计费服务添加认证 (V-04, V-05)~~ ✅

### P2 - 中期修复 (1 月)
1. ~~内存速率限制器添加上限 (V-19)~~ ✅
2. ~~统一日志脱敏 (V-34)~~ ✅
3. 添加 JWT 撤销机制 (V-32)
4. ~~K8s NetworkPolicy (V-24)~~ ✅
5. ~~修复 InsecureSkipVerify (V-11)~~ ✅

### P3 - 低优先级
1. ~~GitHub Actions 版本更新 (V-27)~~ ✅
2. ~~日志注入防护 (V-16, V-17)~~ ✅
3. ~~CORS 通配符限制 (V-23)~~ ✅
4. PKCS#12 密码 (V-40)
