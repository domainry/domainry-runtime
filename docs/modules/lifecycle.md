# Lifecycle 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：retention policy、legal hold、cleanup job、subject request、archive evidence、deletion replay 与 Lifecycle 产品 HTTP
实现：`domainry-lifecycle`

## 当前边界

Lifecycle 的 Domain、Application、persistence、迁移、worker tick 和 17 个产品 HTTP 路由均归 `domainry-lifecycle`。Runtime 只提供宿主数据库、ORM renderer、事务、migration registrar、身份中间件、listener 与通用 HTTP governance，并通过 SDK `Governance`、`System`、`LocalWorkers` 调用业务能力；Runtime 不再取得 Lifecycle repository 或 transaction escape hatch。

Lifecycle Module 通过 `modulehttp.Adapter` 提交 policy、legal hold、cleanup 创建/预览、metrics、archive、subject request、external erasure 与 deletion replay 路由。Runtime 校验并挂载 Adapter，Lifecycle 当前 SDK 源码和契约测试定义精确接口。Runtime Operations 只在自己的命名空间提供 `POST /operations/lifecycle/cleanup/jobs/{jobID}/run`，先写入 durable receipt，再调用 Lifecycle 执行一个 fenced batch；该入口不归 Lifecycle HTTP surface 所有。

Lifecycle 在模块打开时自行向宿主 ledger 提交源表迁移；`EnsureRuntimeSchema` 不再预建或重复登记这些表。retention policy 当前版本与不可变历史复用 Metadata Binding 的共享 `_definitions` / `_definition_versions`（owner=`lifecycle`、kind=`retention_policy`），Lifecycle 不再创建 `_lifecycle_policy_versions`。宿主还必须提供共享 Audit append ports；同一事务 executor 会同时透传给 DefinitionStore 与 Audit，因此 policy publication、业务状态和 `_audit_events` 合规事实共同提交或回滚。Lifecycle 不再创建 `_lifecycle_audit_evidence`，也不把 cleanup progress/failure attempts 等 worker 技术状态写入 Audit；metrics 直接从 cleanup job 聚合读取。`_subject_requests` 以 `request_type` 区分普通请求、account erasure 和 external erasure；account approval 进入主请求 typed payload，external reconciliation 使用同表子行，成功 erase 主行上的 `backup_pending` 与结果引用就是 restore replay authority，因此不再需要 `_lifecycle_account_erasure_approvals`、`_lifecycle_external_erasure_requests`、`_lifecycle_deletion_registry`。所有 owner 的幂等执行计划与结果统一写入共享 `_subject_steps`；主体擦除禁写状态由请求的 indexed `resolved_identity` 和 owner=`lifecycle`、operation=`erase_fence` 的步骤共同表示。Runtime 与 owner 模块直接查询这组状态，不再创建 `_lifecycle_subject_erasure_fences` 或 Lifecycle 私有 step 表。Module 不得创建私有 migration ledger，DDL/DML 使用 `domainry-orm`。

## 代码接入

- Host：`runtime/infrastructure/persistence/lifecyclemodule/host.go`
- Module/owner 组合：`runtime/bootstrap/runtime/service_assembly.go`
- Module Adapter 收集：`runtime/bootstrap/runtime/module_http_adapters.go`
- Runtime-only cleanup orchestration：`runtime/transport/http/lifecycle`
- Runtime 端点契约：`runtime/domain/endpoint` 与对应 HTTP 契约测试

## 已知缺口

- 当前仍由 Runtime bootstrap 固定组装，尚无 Module/SaaS Factory 选择。
- SaaS 化前必须版本化远程 Binding、subject export artifact、legal hold、删除 replay、worker ownership 和不可用策略；不能把本地事务语义直接扩展为跨进程原子性。
