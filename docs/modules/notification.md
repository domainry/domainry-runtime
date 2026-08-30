# Notification 模块

状态：Module 与 SaaS 已接入  
Owner：通知规则、intent/event、inbox、channel delivery、worker 与通知生命周期  
实现/SDK：`domainry-notification` / `domainry-notification-sdk`

## 边界与模式

Runtime 通过 `NotificationFactory` 和 SDK Binding 发布通知、查询/处理 inbox，并提供 recipient、业务资源授权等 callback。Module 借用 Runtime ModuleHost 的数据库、migration、workspace context 与回调；SaaS 通过 Remote Factory 调用独立服务。通知规则和 delivery 状态由 Notification owner 管理，Runtime 不应重新建立通知聚合。

Module migration 必须通过 `ApplyOwnedMigrations`。SaaS publication 交接状态 `runtime_publication_outbox` 已登记为 Runtime workspace-scoped handoff；Notification 自有表不属于 Runtime schema 清单。

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

