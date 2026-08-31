# Audit 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：不可变审计事件、查询、subject lifecycle 与审计保留证据  
实现/SDK：`domainry-audit` / `domainry-audit-sdk`

## 当前边界

Runtime 在 bootstrap 中直接构造 `domainry-audit/module.Factory`，通过 `runtime/infrastructure/persistence/auditmodule.NewHost` 打开 Module Binding。Audit migration 由 Runtime schema 协调器纳入 owner migration。Runtime adapter 将 SDK Binding 适配给现有 Audit append/query/lifecycle port。

Audit 的业务、租户治理和运维 HTTP 路由及 OpenAPI 由 `domainry-audit/internal/transport/http/module` 持有，并通过 Foundation `modulehttp` Surface 交给 Runtime Host 挂载。Runtime 不再在 `records` Handler 或静态 OpenAPI 中复制这些接口。由于业务审计会读取 Runtime Record，Binding 在 Runtime 应用服务完成后通过 Audit SDK `ApplicationHostBinder` 绑定一个窄 Host：Runtime 只负责业务身份补全、Record 数据权限校验和字段级投影；查询范围、留存窗口、脱敏、分页及导出生命周期仍由 Audit owner 负责。

Audit 还提供 `AppendPreparedWithin`，使业务 mutation 与审计事件在同一数据库事务内提交。这一原子性是当前 Module 形态的重要语义，不能在远程化时假装仍由数据库事务保证。

## 代码接入

- Bootstrap：`runtime/bootstrap/runtime/startup.go`、`service_assembly.go`
- Persistence Host/adapter：`runtime/infrastructure/persistence/auditmodule`
- Product application Host：`runtime/bootstrap/runtime/audit_module_host.go`
- Runtime cross-owner Record adapter：`runtime/application/auditbinding/audit_record_access_application_service.go`
- 同事务调用：`runtime/infrastructure/persistence/database/record/record_mutation_effects.go`、`appschema/*store.go`
- Migration：`runtime/infrastructure/persistence/database/runtime_schema.go`
- Module：`domainry-audit/module`
- SDK：`domainry-audit-sdk`

## SaaS 化前置条件

- 在 `runtimehost.Options` 或等价生成组合中建立显式 Factory 注入，不能继续固定构造 Module。
- 用 transactional outbox + immutable event id + receipt/reconciliation 替代跨进程“同事务 append”，并证明业务提交成功后审计最终不会丢失。
- 定义远端认证、workspace/installation scope、顺序、去重、retention/legal hold 和不可用时的 fail policy。
- 增加 Module-only/SaaS-only 外部编译与同一 contract test 后，才可把状态改为“双模式已实现”。

## 变更约束

在上述条件完成前，任何文档或配置不得宣称 Audit 已支持 SaaS 切换。
