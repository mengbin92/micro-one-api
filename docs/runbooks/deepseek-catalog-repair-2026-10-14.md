# DeepSeek 目录缺失与同类供应商编号修复

日期：2026-10-14（Asia/Shanghai）。线上修复从已提交 P1 基线 `6071a995` 构建独立制品；没有把工作区未发布的 P2 契约、报告或迁移一起部署。

## 原因与修复

渠道 11 原为 type=14（后端混元），模型清单为空，没有模型目录映射；旧前端却把 14 显示为 DeepSeek，正确值为 6。受控探测启用后旧自动发现会跳过，而受控任务仅覆盖渠道 1 和已删除的渠道 10。此外原受控库存只保存快照，没有登记目录候选。

同类编号错误还包括 Gemini/Azure 和 SiliconFlow。渠道管理、健康页、概览页已共用 `web/src/lib/channel-providers.ts`，类型值与后端 `domain/upstream/provider/factory.go` 做回归比对：Gemini=3、Azure=5、DeepSeek=6、SiliconFlow=24。既有混元行正确显示为 Hunyuan，不继续冒充 DeepSeek；未安装原生转发的混元不作为新建选项。

更新接口在 admin/channel proto 分别新增 optional type（15/21），通过 service → biz → data 的正常链路写入。省略该字段保留类型；显式非法编号拒绝；凭据、CAS、原因与权限复核保留。空模型清单的渠道允许纠正类型；已有人工模型清单仍不能在 UI 中以空值伪装成已清除。

受控库存由 biz 生成候选 DO，在 data 同一事务保存库存回执、测试/非公开模型和默认禁用的来源映射，覆盖渠道与订阅来源。既有目录状态、价格、公开开关、映射状态和人工清单不被库存覆盖；旧库存不能补写新模型；大小写冲突的未知 upstream ID 只保留库存证据，不合并成新 canonical route。

部署实测另外暴露 P1 兼容解码缺口：含匿名 protobuf 字段的包装 DTO 因方法提升被误认成 proto.Message，protojson.Reset 触发 nil pointer，造成一次 PUT `/api/channel` 返回 502，未落库。已恢复旧 admin API，再加入可复现 red/green 回归并发布 v2：兼容包装通过 jsonx 解码保留 legacy id，直接 protobuf 请求继续 protojson，int64 字符串语义保留。此次不是因修改渠道数据造成的失败。

## 线上操作

- 渠道服务：`docker-compose-channel-service:deepseek-catalog-20261014`，linux/amd64。
- 管理员 API 最终版本：`docker-compose-admin-api:deepseek-catalog-20261014-v2`，linux/amd64。
- 前端：本地构建后上传 `/opt/web/dist`；保留旧 hashed assets 供已有页面刷新过渡。
- 回滚配置：`/opt/micro-one-api/docker-compose/deepseek-catalog-backup-20261014T003929Z`；前端副本 `/opt/web/dist.bak.20261014T003929Z`。
- 管理员现有会话写入渠道 11：type 14→6、revision 2→3。只改供应商编号，解密后 SHA256 比对确认凭据未变，人工 models 仍为空。页面已显示 DeepSeek；没有用直接改库绕过 owner 权限/CAS。
- relay 与 monitor 同步新增 `ch11-inventory`，仅 GET 模型列表：5 分钟调度，最多 300 次/日，成本上界 0，仅 metadata，不新增付费推理任务。保留三个 channel 1 任务原配置，移除不存在的 channel 10 任务。
- 发现配置备份：`/opt/micro-one-api/docker-compose/.env.before-inventory-20261014T005840Z`。

新模型与映射登记独立于公开发布和收费准入。本次目录修复不改价、不启用新收费模型、不改变既有公开模型状态。

## 验证

已验证初始失败：前端 DeepSeek 新建请求发送 14；修改已有空模型渠道缺少供应商编辑入口；repo 修改 DO 类型后仍存 14；库存回执完成后目录 GetModelByID 仍 not found；IAM 兼容包装触发 protobuf.Reset panic。

修复后检查覆盖：前后端 optional presence/CAS/凭据保留，目录测试私有登记、订阅禁用映射、已有价格/状态/映射保留、旧库存拒绝写候选，兼容包装及直接 protojson，race、架构检查、TypeScript、Vite 构建体积预算。独立 P1 制品源码的五个相关 Go package 测试通过；前端相关 33 项通过。

详细文件见 [证据目录](evidence/deepseek-catalog-repair-2026-10-14/deployment.json)。首次真实受控发现与管理员目录展示的结果写入后续现场核验记录。

## GET 模型列表的现场兼容性修复

受控发现配置正确加载，但真实记录为 upstream_rejected/403。同一生产凭据、同一官方 host 的对比确认：GET 带 JSON `null` 为 403；不带 body、保留 Content-Type 为 200；不带 body、Accept=application/json 也为 200，均返回 deepseek-flash 与 deepseek-v4-pro。原探测构造器对 nil map 调用 Marshal，实际产生四字节 null；改为仅在非 nil body 时编码，修复所有库存 GET，不改变付费 canary 的 JSON 构造。httptest 的实际 HTTP seam 已先红（body=[110,117,108,108]）后绿，canary 持久化/去重回归也通过。

## 最终现场验收

受控任务 `ch11-inventory-bodyless` 的 attempt `canary_4e728291-69c6-4ac4-8559-45742a81232a` 返回 inventory_observed / HTTP 200，库存为 deepseek-flash、deepseek-v4-pro。新目录模型 deepseek-flash ID=519、status=2（测试中）、is_public=0；渠道 11 两条映射均 enabled=0。已在现有管理员页面确认模型目录第一行显示 deepseek-flash 与“测试中”。临时 1 分钟核验周期已恢复 5 分钟，来源日预算未扩大；公开发布、映射启用和收费 profile 仍为独立审核步骤。

最终 relay 镜像 `docker-compose-relay-gateway:deepseek-inventory-20261014`；模型库存 HTTP 请求修复仍从隔离 P1 基线构建。期间曾失败的 GET null body 与一次管理员 502 保留为诊断事实，最终验收不使用失败结果代替成功。迁移未包含 P2。
