# Report 模块

状态：独立源码 Module 已接入；Runtime 支持显式 Module/SaaS Factory 拓扑
Owner：Report definition repository、snapshot/run 状态与 Report 自有持久化  
实现/SDK：`domainry-report` / `domainry-report-sdk`

## 当前边界

Runtime 从 Manifest 投影 Report、operation state example、sensitive-field policy 与 export control 定义，并通过 SDK `DefinitionRepository` 同步给 Report owner。Module 借用宿主数据库、context-aware DBTX、SQL 方言和 migration registrar，复用宿主唯一 `_schema_migrations`。Report Module 独占 snapshot DML、幂等 claim、lease 与 fencing；Runtime 只保留 SDK model adapter、跨 owner 授权、Record 数据读取和 Notification 编排，不再实现 Report snapshot store。

## 代码接入

- Bootstrap：`runtime/bootstrap/runtime/startup.go`
- Host 与定义同步：`runtime/bootstrap/runtime/report_module_host.go`
- Runtime query adapter：`runtime/application/report`
- Module/SDK：`domainry-report/module`、`domainry-report-sdk`
- 拓扑选择：`runtimehost.Options.ReportFactory`

## SaaS 运维约束

- Module 模式通过 `Host.DatabaseFor(ctx)` 继承宿主事务，使 snapshot terminal transition 与 Notification inbox 原子提交。
- SaaS Factory 必须实现 Report protocol v2 的 `definitions.sync` 与 `snapshots.manage` capability，并保持相同的 idempotency/fencing 语义。
- SaaS 模式不能伪装成本地事务；跨服务 Notification、large result/artifact 的 receipt/outbox 与 reconciliation 属于部署实现的运维责任，并必须在切流前完成验证。
