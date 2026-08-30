# Lifecycle 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：retention policy、legal hold、cleanup job、subject request、archive evidence 与 deletion replay 状态  
实现：`domainry-lifecycle`

## 当前边界

Lifecycle persistence 与业务状态归 `domainry-lifecycle`。Runtime 提供宿主数据库、ORM renderer、事务边界和 migration registrar，并保留 `/operations/lifecycle/*` 的授权、Operations receipt 与 HTTP 组合。Module 不得创建私有 migration ledger；DDL/DML 使用 `domainry-orm`。

## 代码接入

- Host：`runtime/infrastructure/persistence/lifecyclemodule/host.go`
- Runtime application binding：`runtime/application/lifecycle/binding.go`
- HTTP adapter：`runtime/transport/http/lifecycle/lifecycle_handler.go`
- Bootstrap：`runtime/bootstrap/runtime/service_assembly.go`

## 已知缺口

- 当前仍由 Runtime bootstrap 固定组装，尚无 Module/SaaS Factory 选择。
- schema assembler、management connection 与 migration failure seam 必须在模块迁移后重新通过完整契约测试。
- SaaS 化前必须设计 subject export artifact、legal hold、删除 replay 和不可用策略，不能把本地事务语义直接扩展为跨进程原子性。
