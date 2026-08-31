# Integration Module / SaaS boundary

状态：SDK、source-owned Module migration boundary 与 Runtime publication handoff
均已接入。Runtime 生产依赖图不再包含 Integration 管理面、Webhook inbox、
Provider、Invocation reconciliation、Credential worker 或旧 Integration persistence。

Runtime 只永久拥有 `_publication_outbox`。该表是 Record、Action、
Workflow 本地事务提交外部副作用的 durable handoff。Connector 的源定义、
Provider schema 与实现归 `domainry-connectors`；`domainry-integration` 消费并
物化该 catalog，同时拥有 connection、credential、webhook inbox、event mapping、
Provider task state 和 invocation evidence。Web Push 的 readiness、浏览器
subscription、endpoint、`p256dh` 与 auth material 同样归 Integration；Runtime
只在 `_publication_outbox` 保存公开的 `subscription_id` 和通知 payload。

应用 Manifest 的连接声明通过 Binding `Requirements.SynchronizeConnections`
交给 owner。Module 写入自己的 `_integration_connections`，SaaS 调用远端
`/v1/application-requirements/connections`；Runtime 不再直接写该表，也不再
创建或读取 `_application_schema_connector_requirements`。Connector catalog
只在启动与在线校验时经 Integration SDK 投影到内存；应用侧持久化的只是连接
声明与 event mapping requirement，owner 的物化定义位于
`_integration_connector_definitions`。

Module 通过窄 Host 借用数据库、SQL dialect 和 migration registrar，owner
`integration` 的 migrations 写入宿主唯一 `_schema_migrations`。SaaS Binding
在远端持有 owner 状态；Runtime Outbox 使用稳定 message id/dedup key 交接，
只保存远端 receipt reference，不复制 invocation 状态。Runtime 对不确定网络结果
重试的是幂等 `Delivery.Accept`，不是 Provider 调用；Provider 的不确定结果与
reconciliation 完全留在 Integration owner 内。

Web Push 发送时，Integration owner 在 Provider 调用前使用 `subscription_id`
解析 endpoint/key material。解析后的敏感材料不回写 Runtime handoff，也不进入
Integration invocation evidence。Module 从 owner 表解析，SaaS 通过同一
`WebPushSubscriptions` Binding 在远端管理与解析。

入站 SaaS event 使用稳定 event/mapping identity 调用 Runtime Action 或
Workflow，接收端依靠自身 receipt 幂等，Runtime 不镜像 Integration inbox。

Runtime 中仍可出现 `connector_key`、operation 与 Integration DTO，但只限三类：
应用 Action/Workflow/Automation 的外部能力引用校验、平台 capability discovery
的只读 JSON Schema 投影，以及 `_publication_outbox` handoff。它们不定义
Connector 产品、不保存 connection/credential/invocation，也不执行 Provider。
