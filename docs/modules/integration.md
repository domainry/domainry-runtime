# Integration Module / SaaS boundary

状态：SDK、source-owned Module migration boundary 与 Runtime publication handoff
均已接入。Runtime 生产依赖图不再包含 Integration 管理面、Webhook inbox、
Provider、Invocation reconciliation、Credential worker 或旧 Integration persistence。

Runtime 只永久拥有 `_publication_outbox`。该表是 Record、Action、
Workflow 本地事务提交外部副作用的 durable handoff。Connector 的源定义、
Provider schema 与实现归 `domainry-connectors`；`domainry-integration` 消费并
将 catalog 以 owner=`integration`、kind=`integration_connector` 写入共享
`_definitions` / `_definition_versions`，同时拥有 connection、credential、webhook inbox、event mapping、
Provider task state 和 invocation evidence。Web Push 的 readiness、浏览器
subscription、endpoint、`p256dh` 与 auth material 同样归 Integration；Runtime
只在 `_publication_outbox` 保存公开的 `subscription_id` 和通知 payload。

应用 Manifest 的连接声明通过 Binding `Requirements.SynchronizeConnections`
交给 owner。Module 写入自己的 `_integration_connections`，SaaS 调用远端
`/v1/application-requirements/connections`；Runtime 不再直接写该表，也不再
创建或读取 `_application_schema_connector_requirements`。Connector catalog
在启动时通过共享 Definition source snapshot 原子替换；应用侧持久化的连接
声明仍是 Integration owner state，event mapping requirement 则以
owner=`integration`、kind=`integration_event_mapping` 的 workspace source
snapshot 写入同一个共享 Definition store。Integration 不再创建私有 connector
或 event-mapping definition 表。

Module 通过窄 Host 借用数据库、SQL dialect、migration registrar 和 Metadata
DefinitionStore，owner
`integration` 的 migrations 写入宿主唯一 `_schema_migrations`。SaaS Binding
由 SaaS host 提供相同 DefinitionStore contract 并在远端持有 owner 状态；Runtime Outbox 使用稳定 message id/dedup key 交接，
只保存远端 receipt reference，不复制 invocation 状态。Runtime 对不确定网络结果
重试的是幂等 `Delivery.Accept`，不是 Provider 调用；Provider 的不确定结果与
reconciliation 完全留在 Integration owner 内。

Web Push 发送时，Integration owner 在 Provider 调用前使用 `subscription_id`
解析 endpoint/key material。解析后的敏感材料不回写 Runtime handoff，也不进入
Integration invocation evidence。Module 从 owner 表解析，SaaS 通过同一
`WebPushSubscriptions` Binding 在远端管理与解析。

入站 SaaS event 使用稳定 event/mapping identity 调用 Runtime Action 或
Workflow，接收端依靠自身 receipt 幂等，Runtime 不镜像 Integration inbox。

Integration 的主体擦除计划与结果只写 Lifecycle 安装的共享
`_subject_requests` / `_subject_steps`。嵌入式 Module 的 persistence binder 仅在
Lifecycle 完成迁移和 owner 绑定后由 Runtime 启用，并先校验共享表列契约；SaaS
remote Binding 不向 Runtime 暴露本地 binder。Integration SaaS owner 必须在其
数据库已安装同一共享 schema 后显式绑定，缺表时启动失败，不创建私有 fallback
表，也不 dual write。

Runtime 中仍可出现 `connector_key`、operation 与 Integration DTO，但只限三类：
应用 Action/Workflow/Automation 的外部能力引用校验、平台 capability discovery
的只读 JSON Schema 投影，以及 `_publication_outbox` handoff。它们不定义
Connector 产品、不保存 connection/credential/invocation，也不执行 Provider。
