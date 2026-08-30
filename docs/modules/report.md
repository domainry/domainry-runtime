# Report 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：Report definition repository、snapshot/run 状态与 Report 自有持久化  
实现/SDK：`domainry-report` / `domainry-report-sdk`

## 当前边界

Runtime 从 Manifest 投影 Report、operation state example、sensitive-field policy 与 export control 定义，并通过 SDK `DefinitionRepository` 同步给 Report owner。Module 借用宿主数据库、SQL 方言和 migration registrar，复用宿主唯一 `_schema_migrations`。Runtime 保留跨 owner 授权、Record 数据读取和 Notification 编排，不拥有 Report snapshot 表。

## 代码接入

- Bootstrap：`runtime/bootstrap/runtime/startup.go`
- Host 与定义同步：`runtime/bootstrap/runtime/report_module_host.go`
- Runtime query adapter：`runtime/application/report`
- Module/SDK：`domainry-report/module`、`domainry-report-sdk`

## 已知缺口

- `runtimehost.Options` 尚无 Report Factory，当前只支持固定 Module 组装。
- Runtime 旧 `_report_snapshots` store/tests 必须清退或改为通过 Report Module fixture 验证，不能要求 Runtime schema 重建 owner 表。
- SaaS 化前必须定义数据读取授权、large result/artifact 交接、snapshot fencing、Notification receipt 与失败 reconciliation。
