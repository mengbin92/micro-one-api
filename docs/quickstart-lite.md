# SQLite Lite：从空目录到第一个 Relay 请求

Lite 保留现有 9 个服务，用 SQLite 文件卷替代 MySQL，另运行一个 Redis。
适合单机、低并发的个人或小团队使用。本文验证入口是
[`scripts/test-lite-smoke.py`](../scripts/test-lite-smoke.py)：真实启动各服务，通过 HTTP
登录、创建渠道、充值测试钱包、创建 Token、列出模型并完成聊天，最后验证重启后数据仍在。
只有上游模型响应由本地 mock 提供，不需要真实供应商凭证。

这里的「10 分钟」指镜像就绪后的配置和首次请求操作。当前 lite compose 从源码构建，
首次下载依赖和编译另计，不能承诺从 clone 起总耗时 10 分钟。v0.26.6 及更早版本的
compose 不包含这些修复，请使用 v0.27.0 或包含本文的更新代码。

## 1. 准备与启动

需要 Git、Docker Engine / Docker Desktop、Docker Compose 插件、Python 3（生成密钥）。
本地安装 Go、Node、MySQL 均不是前置条件，构建在 Docker 内完成。
请先为 Docker 分配至少 4 CPU、8 GiB 内存及 20 GiB 可用磁盘用于首次源码构建。
运行时可从 2 CPU / 2 GiB 的单机预算起步，再按请求并发和日志保留量调整；这是部署预算建议，
不是经过压测的最低容量保证。SQLite 不适合多主机共享文件或高并发写入。

```bash
git clone https://github.com/mengbin92/micro-one-api.git
cd micro-one-api/deployments/docker-compose
cp .env.lite.example .env
python3 - <<'PY'
from pathlib import Path
import secrets
p = Path('.env')
values = {
    'JWT_SECRET_KEY': secrets.token_hex(32),
    'SERVICE_TOKEN': secrets.token_hex(32),
    'ADMIN_TOKEN': secrets.token_hex(32),
    # AES key is the literal string: 16 random bytes become 32 ASCII bytes.
    'CHANNEL_ENCRYPTION_KEY': secrets.token_hex(16),
    'REDIS_PASSWORD': secrets.token_hex(32),
}
p.write_text('\n'.join(
    f'{line.split("=", 1)[0]}={values[line.split("=", 1)[0]]}'
    if '=' in line and line.split('=', 1)[0] in values else line
    for line in p.read_text().splitlines()
) + '\n')
p.chmod(0o600)
PY

# 后续命令均在此目录运行；每个新终端重新定义此函数。
lite() { docker compose -p oneapi-lite --env-file .env -f docker-compose.lite.yml "$@"; }
lite config --quiet
# 按服务串行构建；仅设置 COMPOSE_PARALLEL_LIMIT 不能限制 BuildKit 的并行编译。
for service in migrate identity-service notify-worker channel-service billing-service config-service log-service monitor-worker relay-gateway admin-api; do
  lite build "$service"
done
lite up -d
lite ps -a
lite logs migrate
```

`sqlite-init` 和 `migrate` 应为 `Exited (0)`，Redis 应为 healthy，其余服务保持 running。
`sqlite-init` 只初始化共享卷目录权限，迁移与服务均以 UID 65534 访问 SQLite。
后续启动会检查并应用尚未执行的增量迁移。

| 入口 | 默认地址 | 端口被占用时修改 `.env` |
|---|---|---|
| 管理后台 | `http://127.0.0.1:3000` | `LITE_ADMIN_PORT=13000` |
| Relay | `http://127.0.0.1:8080` | `LITE_RELAY_PORT=18080` |
| 健康检查 | `http://127.0.0.1:8080/healthz` | 使用相同 Relay 端口 |

默认只监听本机 `127.0.0.1`。远程试用可用 SSH 转发上述端口；公开服务时配置 HTTPS
反向代理，并明确设置 `LITE_BIND_ADDRESS`。Redis 和内部服务不发布宿主机端口。
不同实例使用不同 Compose project 名称和宿主机端口。

## 2. 登录与创建渠道

首次启动、用户表为空时创建 `admin`。默认随机密码写入 SQLite 卷内的私有文件，
不会在服务日志中打印。仅在自己的终端读取并存入密码管理器：

```bash
lite exec -T identity-service cat /data/initial-admin-password.txt
```

打开管理后台登录。也可以在**首次启动前**设置 `.env` 的 `INITIAL_ADMIN_PASSWORD`；
这种情况下不生成密码文件。已有用户时修改该变量不会重置密码。
更改密码后可删除初始密码文件；恢复账号请使用 [管理员重置工具](../cmd/admin-reset/main.go)。

进入 **管理后台 → 渠道 → 创建渠道**：

| 字段 | 填写方式 |
|---|---|
| 名称 | 例如 `my-provider` |
| 供应商 | OpenAI-compatible 上游选择 OpenAI |
| 基础 URL | 供应商提供的 API 根地址，例如 `https://api.example.com/v1`；不填写完整 `/chat/completions` 路径 |
| API 密钥 | 自己向供应商申请的上游 API Key |
| 模型 | 供应商实际支持的模型 ID；多个模型按页面提示分隔 |
| 分组 | `default`，与当前用户分组一致 |
| 优先级 / 权重 | 保留默认值 |

保存后执行渠道 **测试**。真实上游测试会消耗供应商额度。失败时先核对 URL、模型 ID、
凭证和供应商账户余额。容器里的 `localhost` 指容器自身；不要用它指代宿主机模型服务。
自动 smoke 的私网 mock 例外仅存在于临时 override，正式配置保留 SSRF 检查。

## 3. 钱包与 Token

进入 **仪表盘**，确认调用用户的钱包余额足够。默认首次管理员有初始测试额度。
余额不足时，进入 **管理后台 → 兑换码 → 创建兑换码**，填写名称、金额（例如 `1.00`）
和数量 `1`，保存后复制生成的兑换码；再进入用户侧 **兑换码** 页面，粘贴并点击
**立即兑换**。回到仪表盘确认到账。此操作给本地钱包记账，供应商账户余额仍需单独充值。
API Token 的「不限额度」仅取消 Token 自身限制，不会免除钱包扣费。

进入 **API 密钥 → 创建 Token**，填写名称，创建后立即安全保存完整 Token。
上游 API Key 用于渠道，Relay Token 用于下面的客户端请求，二者不能互换。

在 Bash 终端中读取 Token，避免把它直接写进命令历史：

```bash
read -r -s -p 'Relay Token: ' API_TOKEN
printf '\n'
export API_TOKEN
export RELAY_URL=http://127.0.0.1:8080
export MODEL='<渠道中配置的真实模型 ID>'
curl --fail-with-body -H "Authorization: Bearer ${API_TOKEN}" \
  "${RELAY_URL}/v1/models"
curl --fail-with-body "${RELAY_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}],\"max_tokens\":16,\"stream\":false}"
unset API_TOKEN
```

成功时先得到模型列表，再得到 `choices[0].message.content` 和 `usage`。
在 **用量记录** 中确认请求；钱包余额不足时先补余额，再重试。
网页 Playground 是可选入口：跨域调用时把**精确的控制台 origin** 写入
`CORS_ALLOWED_ORIGINS`，例如 `http://127.0.0.1:3000`，再重建 relay 容器。

## 4. 保存密钥、备份与恢复

SQLite 位于 `oneapi-lite_sqlite_data` 卷，含数据库、WAL/SHM 和初始密码文件；
Redis 位于 `oneapi-lite_redis_data` 卷。`.env` 中的 `CHANNEL_ENCRYPTION_KEY` 必须与数据库
一起保存，丢失后无法解密原渠道凭证。不要在重启或升级时重新生成密钥。
`.env`、备份和 Token 都不要提交到 Git。

低并发单机建议采用短暂停机备份，确保 SQLite、Redis 及待处理任务处于同一停机点。
先暂停客户端流量，等待正在执行的请求和异步计费队列归零，再执行：

```bash
umask 077
backup_dir="$(pwd)/backup-$(date +%Y%m%d-%H%M%S)"
mkdir "$backup_dir"
lite stop
lite run --rm -T --no-deps sqlite-init tar -C /data -czf - . > "$backup_dir/sqlite.tar.gz"
lite run --rm -T --no-deps redis tar -C /data -czf - . > "$backup_dir/redis.tar.gz"
cp .env "$backup_dir/env"
git rev-parse HEAD > "$backup_dir/revision.txt"
lite up -d
```

将备份复制到另一块磁盘/主机。不要只复制运行中的 `.db` 文件，也不要执行 `down -v`，
它会删除持久卷。首次备份后应在另一个 project 名称、空卷和空闲端口上做恢复验证。

恢复时使用备份记录的代码版本、`.env` 和**空卷**，定义对应 project 的 `lite` 函数，
在服务启动前解包（`backup_dir` 指向备份目录）：

```bash
lite run --rm -T --no-deps sqlite-init tar -C /data -xzf - < "$backup_dir/sqlite.tar.gz"
lite run --rm -T --no-deps redis tar -C /data -xzf - < "$backup_dir/redis.tar.gz"
lite up -d
```

检查登录、渠道和原 Token 可用。不要将旧备份覆盖到仍在写入的卷，也不要只恢复 SQLite
后沿用另一个时点的 Redis 队列。

## 5. 升级与边界

先读目标版本发布说明并完成上面的停机备份，再切到目标 tag/提交：

```bash
lite stop
# 在仓库中切换至已审阅的目标 tag/提交，保留原 .env。
# 按服务串行构建；仅设置 COMPOSE_PARALLEL_LIMIT 不能限制 BuildKit 的并行编译。
for service in migrate identity-service notify-worker channel-service billing-service config-service log-service monitor-worker relay-gateway admin-api; do
  lite build "$service"
done
lite run --rm migrate
lite up -d
lite logs migrate
```

lite 的管理前端由 admin 镜像自带 `/web`，随 `admin-api` 镜像更新；它与主生产 Compose
使用宿主机 `/opt/web/dist` 挂载的方式不同。升级失败时按发布说明回退镜像；若迁移已改变
schema，不要直接做 down migration，使用一致的停机备份恢复数据库、Redis 和原版本。
SQLite/Postgres 的 `011` 补齐渠道列，`090` 修复旧库账务时间类型；SQLite `090` 在事务内
重建相关表，保留金额、ID、自增序列和索引。升级前停止服务并备份，预留至少一份相关表的
额外磁盘空间。它们是方言专用迁移，不改变 MySQL schema。
本指南保持 executor 默认关闭、canonical observe 默认值，不包含 charge 灰度或历史冲正。
分组重设计（路由分组 / 订阅合约 / 有序候选组等，迁移 092–100 由 lite 的 migrate 容器
自动应用）的全部运行时开关在 Lite 中同样默认关闭：不设置即保持旧行为。如需试用，
开关清单与启用顺序见 [runbooks/routing-groups-runbook.md](./runbooks/routing-groups-runbook.md)
（Lite 用 `--driver=sqlite3` 方言，无需 per-schema `-ownership` 步骤）。

## 6. 可重复的空环境 smoke

在仓库根目录运行：

```bash
python3 scripts/test-lite-smoke.py
```

脚本复制当前受 Git 管理的源码到全新临时目录，生成独立测试密钥，分配唯一 project 名称、
随机本机端口和全新卷，构建时自动生成 proto。它不会读取现有 `.env`，不会复用生产卷，
不会请求外部模型。成功输出逐项 PASS，默认自动清理本次容器、镜像、网络和卷。
`--keep` 可保留隔离环境用于人工检查，末尾会输出该环境的清理命令；诊断日志保存在
私有临时目录，可能含测试数据，不应直接公开上传。

本阶段的脱敏界面截图位于 [`docs/assets/screenshots`](./assets/screenshots)，完整操作录屏位于
[`lite-quickstart.mp4`](./assets/demos/lite-quickstart.mp4)。录屏使用临时管理员、临时 Token
和本地 mock 上游，字段中的凭证经过遮蔽；它不是生产环境性能或安全证明。
