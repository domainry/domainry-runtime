# Report 模块

状态：独立源码 Module 已接入；Runtime 通过显式 Factory 支持 Module/SaaS 拓扑
Owner：Report 定义仓、查询与 Object SQL 业务规则、快照运行状态、导出策略和产品 HTTP Surface
实现/SDK：`domainry-report` / `domainry-report-sdk`

## 当前边界

Runtime 从 Manifest 生成 Report、operation state example、sensitive-field policy 与 export control 投影，并只通过 SDK `DefinitionRepository` 同步给 Report owner。同步完成后，在线定义读取以 Report 自有仓储为准，Runtime 的 Application Schema 不再作为 Report 查询或导出准备的读取端口。

Report 通过 SDK `ApplicationBinding` 独占以下用例与规则：

- summary 与 Object SQL 查询、字段投影、稳定分页和 source-version fencing；
- snapshot refresh、幂等 claim、lease、fencing、重试和 Report 自有 DML；
- export scope、声明式 query/tag predicate、字段投影、masking、定义/授权哈希与 prepare 语义；
- 四条产品 HTTP：summary、Object SQL query、snapshot refresh、export prepare，以及对应 OpenAPI/governance 声明。

Runtime 不再发布 Report handler、静态 OpenAPI path、endpoint policy、第二套 query/snapshot application service 或 `ReportDomainService` 执行引擎。Runtime 保留的代码必须属于宿主或跨 owner 边界：身份解析，授权后的 Record/SQL 读取，source version，execution audit，snapshot terminal 与 Notification 的原子提交，以及 Report export 与 Audit、Record、Data Exchange 的编排适配。Report 负责 fallback join、cardinality、filter、aggregate、analysis、Object SQL 规划和全部 export policy；Runtime adapter 只回答当前 Record/field 授权事实并执行 Report 已编译的 SQL plan。Data Exchange 的 Report worker 通过 SDK `Exports.ResolveExecution`、`ReadPage`、`SourceVersion` 完成当前定义解析、scope 授权、分页执行和 fencing；不得从 Runtime Application Schema、Manifest 副本或 Runtime 自建 Report 引擎读取/执行。

导出 prepare 成功后，Data Exchange 是唯一 job/artifact owner。查询、取消和下载走 `/data-exchange/jobs/*`；不得恢复 `/report-exports/*` 生命周期别名。Report 的 prepare 响应 `Location` 指向 Data Exchange 的规范任务地址。

## 代码接入

- Bootstrap、定义同步与 ApplicationHost：`runtime/bootstrap/runtime/report_module_host.go`、`runtime/bootstrap/runtime/startup.go`
- Host anti-corruption adapter：`runtime/application/report/adapter/report_module_host.go`
- 跨 owner export adapter：`runtime/application/report/export`
- SDK application 绑定：`runtime/bootstrap/composition/report_application_binding.go`
- Module/SDK：`domainry-report/module`、`domainry-report-sdk`
- 拓扑选择：`runtimehost.Options.ReportFactory`

## Module/SaaS 运维约束

- Factory 必须通过 Report protocol v3 校验，并声明 `definitions.sync`、`queries.execute`、`snapshots.manage`、`exports.manage` 与 `http.surface`。
- Module 模式通过 `Host.DatabaseFor(ctx)` 继承宿主事务，使 snapshot terminal transition 与 Notification inbox 原子提交，并复用宿主唯一 `_schema_migrations` ledger。
- SaaS 模式不能伪装成本地事务；跨服务 Notification 与 Data Exchange 协作必须显式实现 receipt/outbox、幂等、重试和 reconciliation，并在切流前验证同一业务语义。
