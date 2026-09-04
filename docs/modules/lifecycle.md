# Lifecycle 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：retention policy、legal hold、cleanup job、subject request、archive evidence、deletion replay 与 Lifecycle 产品 HTTP
实现：`domainry-lifecycle`

## 当前边界

Lifecycle 的 Domain、Application、persistence、迁移、worker tick 和 17 个产品 HTTP 路由均归 `domainry-lifecycle`。Runtime 只提供宿主数据库、ORM renderer、事务、migration registrar、身份中间件、listener 与通用 HTTP governance，并通过 SDK `Governance`、`System`、`LocalWorkers` 调用业务能力；Runtime 不再取得 Lifecycle repository 或 transaction escape hatch。

Lifecycle Module 通过 `modulehttp.Adapter` 提交 policy、legal hold、cleanup 创建/预览、metrics、archive、subject request、external erasure 与 deletion replay 路由及其 OpenAPI。Runtime 校验并挂载 Adapter。Runtime Operations 只在自己的命名空间提供 `POST /operations/lifecycle/cleanup/jobs/{jobID}/run`，先写入 durable receipt，再调用 Lifecycle 执行一个 fenced batch；该入口不归 Lifecycle HTTP surface 所有。

Lifecycle 在模块打开时自行向宿主 ledger 提交 `_lifecycle_*` 迁移；`EnsureRuntimeSchema` 不再预建或重复登记这些表。Module 不得创建私有 migration ledger，DDL/DML 使用 `domainry-orm`。

## 代码接入

- Host：`runtime/infrastructure/persistence/lifecyclemodule/host.go`
- Module/owner 组合：`runtime/bootstrap/runtime/service_assembly.go`
- Module Adapter 收集：`runtime/bootstrap/runtime/module_http_adapters.go`
- Runtime-only cleanup orchestration：`runtime/transport/http/lifecycle`
- Runtime OpenAPI merge：`runtime/transport/http/openapi/openapi.go`

## 已知缺口

- 当前仍由 Runtime bootstrap 固定组装，尚无 Module/SaaS Factory 选择。
- SaaS 化前必须版本化远程 Binding、subject export artifact、legal hold、删除 replay、worker ownership 和不可用策略；不能把本地事务语义直接扩展为跨进程原子性。
