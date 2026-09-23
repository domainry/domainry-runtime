# Notification 模块

状态：Module 与 SaaS 已接入  
Owner：通知规则、intent/event、inbox、channel delivery、worker 与通知生命周期  
实现/SDK：`domainry-notification` / `domainry-notification-sdk`

## 边界与模式

Runtime 通过 `NotificationFactory` 和 SDK Binding 发布通知、查询/处理 inbox，并提供 recipient、业务资源授权等 callback。Module 借用 Runtime ModuleHost 的数据库、migration、workspace context 与回调；SaaS 通过 Remote Factory 调用独立服务。通知规则和 delivery 状态由 Notification owner 管理，Runtime 不应重新建立通知聚合。

Notification 的模板管理、Inbox、偏好、治理与 SSE 产品 HTTP 由 Notification 实现仓的 `internal/transport/http/module` 统一持有。Module Binding 直接暴露 `modulehttp.Provider`；SaaS 组合由 `notificationmodule.NewSaaSFactory(notificationremote.NewFactory(...))` 把同一 Adapter 装配到 Remote Binding，前端路径不随拓扑变化。Runtime 只负责通用 listener、认证 guard 与 Adapter 挂载；owner 当前 SDK/客户端源码和契约测试定义精确接口。跨 Runtime 业务资源二次鉴权的两个 action-resolution 路由和 Integration publication ledger 查询仍留在 Runtime。

Module migration 必须通过 `ApplyOwnedMigrations`。Delivery policy 通过宿主的共享 DefinitionStore 持久化为 owner `notification`、kind `delivery_policy` 的 workspace 定义并使用 Definition revision CAS。模板根记录与不可变发布版本也使用同一个 DefinitionStore，分别为 kind `notification_template` 和 `notification_template_version`；发布动作在同一事务内写入不可变版本并推进根记录 CAS，不再创建 Notification 私有模板表。模板发布请求使用共享 `_operations` 中 owner `notification`、kind `template_publication` 的行；模板根 Definition 上的 CAS reservation 与 Operation 创建/终态释放同事务提交，定时发布仍使用共享 Operation 的 lease/fencing，不再需要私有 request/lock 表。workspace freeze/import/cutover 状态使用共享 `_operation_controls` 中 system purpose `notification_migration`、kind `workspace_cutover` 的 revision-fenced 行，不再创建 `_notification_migration_controls`。retention payload 通过 Lifecycle ArchiveStore 归档，Module 在 Lifecycle 挂载后绑定共享 store，未挂载时 fail closed；Notification 不再拥有归档表，也不把共享归档行放入 portable bundle。SaaS 在同一物理库和唯一 `_schema_migrations` ledger 上打开相同 Metadata、Operations 与 Lifecycle archive store。SaaS publication 交接状态 `_publication_outbox` 已登记为 Runtime workspace-scoped handoff；Notification 自有表不属于 Runtime schema 清单。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Host/组装：`runtime/bootstrap/runtime/notification_sdk_module_host.go`、`runtime/bootstrap/runtime/startup.go`
- Runtime callback：`runtime/bootstrap/runtime/notification_startup_bindings.go`
- SDK：`domainry-notification-sdk/sdk.go`、`modulehost`、`remote`、`contracttest`
- Module/SaaS：`domainry-notification/module`、`internal/assembly/saas`、`cmd/notification-server`

## 变更约束

- 新通知 channel 属于 Notification，不得在 Runtime 增加 channel worker。
- Module 与 SaaS 必须共享 intent 幂等、inbox action 授权和稳定错误语义。
- Remote retry 不能造成重复发送；使用业务 idempotency/receipt，不依赖 HTTP retry 恰好一次。
- 切 SaaS 前必须排空/迁移 publication outbox 并核对未完成 delivery。
