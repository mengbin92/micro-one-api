# RBAC 资源服务 identity 地址遗漏导致线上 RPC 失败

2026-10-01 · 排查分支 `develop`，本地 HEAD `3dd4736d` · 最终复查 22:18:04 Asia/Shanghai。

## 现象与复现

更新后九个业务容器均为 running，自动重启次数为 0，健康接口返回 200，但业务请求失败。relay 日志中的分组查询、路由审计、计费提交，以及 admin 的定时查询均出现 `dial tcp 127.0.0.1:9001: connect: connection refused`。

使用容器现有管理凭据请求 `GET /api/channel/?page=1&page_size=1`，修复前稳定返回 HTTP 500。用户列表返回 200，表明并非所有 identity 客户端配置都失效。验证期间不输出凭据或资源内容。

## 根因

`29009419` 引入六个资源服务的 owner 权限检查。这些检查通过 `platform/authz.FromEnvironment` 向 identity 查询当前授权模式与权限；即使处于 legacy 模式也需要 identity 可用。

线上 channel、billing、config、log、monitor、notify 容器都没有 `IDENTITY_GRPC_ENDPOINT`，因而回退至容器自己的 `127.0.0.1:9001`。容器间服务名称解析与 RPC 端口连通正常，admin 和 relay 自身的 identity 地址也正确；错误实际来自下游 owner 的 identity 调用。

当前分支主 Compose 已为六个服务加入 `IDENTITY_GRPC_ENDPOINT=identity-service:9001`，但线上 Compose 未同步。`scripts/deploy-update.sh` 上传镜像后使用服务器已有 Compose 重建容器，不同步仓库的 Compose 修改。

## 修复

1. 备份线上文件到 `/opt/micro-one-api/docker-compose/docker-compose.yml.bak.authz-endpoint-20261001-221309`，备份权限 0600。
2. 仅在六个资源服务的 environment 中加入 `IDENTITY_GRPC_ENDPOINT=identity-service:9001`。通过 `docker compose config --quiet` 校验，并比较修改前后的有效 Compose JSON，确认没有其他有效配置变更。
3. 核对 latest 镜像与运行镜像相同，以 `docker compose up -d --no-deps --no-build --pull never --force-recreate` 重建六个服务，复核镜像摘要保持一致。
4. 资源容器重建后部分 IP 重新分配，relay 随后的定时任务仍报告旧 channel IP 连接拒绝。优雅重启 relay，刷新下游连接；其镜像保持一致。

## 验证与边界

最终复查用户、渠道、路由分组、日志、支付订单列表和账务账户六个只读接口，全部返回 200；原始渠道列表复现转绿。九个业务服务的 `/healthz` 均返回 200，运行正常，自动重启次数为 0（不代表未进行人工重启）。relay 重启后的采样窗口内，relay 与 admin 日志中 `connection refused` 次数为 0。

本次没有构建或拉取镜像，没有数据库迁移、授权模式切换或付费模型请求。真实模型调用和定时任务下一周期没有纳入这次只读验证。脱敏的接口结果与实际镜像摘要见 [验证证据](evidence/rbac-owner-endpoint-recovery-2026-10-01.json)。

## 回滚与预防

备份保存的是导致本次故障的配置。若确需回滚，应将六个资源服务的镜像与配套配置一起回退；单独恢复该备份会再次导致当前镜像的权限查询失败。

后续包含 Compose 环境变量修改的发布必须审阅并同步服务器配置；仅上传镜像不足以完成发布。部署前应校验资源服务的有效 identity 地址，部署后应验证业务 RPC 路径，不能只检查容器 running 或 `/healthz`。
