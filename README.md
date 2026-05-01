# micro-one-api

基于 `one-api` 的微服务化重构方案仓库，当前主要沉淀：

1. 现有 `one-api` 项目的结构分析。
2. 微服务拆分与迁移路径设计。
3. 基于 `go-kratos` 的落地架构方案。
4. 第一阶段 Kratos 工程骨架、proto 契约与 gRPC 调用链落地。

## 文档

1. [One-API微服务改造方案](./docs/One-API微服务改造方案.md)
2. [One-API基于Kratos的微服务落地方案](./docs/One-API基于Kratos的微服务落地方案.md)
3. [第一阶段实施方案](./docs/第一阶段实施方案.md)

## 当前结构

当前仓库已按最新 `go-kratos/kratos-layout` 风格组织为：

1. `api/`
2. `cmd/`
3. `configs/`
4. `internal/`
5. `third_party/`

## 鸣谢

感谢 [one-api](https://github.com/songquanpeng/one-api) 项目提供的原始架构与实现基础，本仓库中的分析与改造方案均基于该项目展开。

感谢 [go-kratos/kratos](https://github.com/go-kratos/kratos) 项目提供的微服务框架设计与工程实践参考。
