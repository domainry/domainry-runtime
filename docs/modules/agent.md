# Agent 模块

状态：SDK、Module、SaaS Binding 与独立 SaaS 服务入口已接入  
Owner：模型/provider 执行、协议适配、provider run、模型路由与原始 usage 证据  
实现/SDK：`domainry-agent` / `domainry-agent-sdk`

## 边界与模式

Runtime 继续拥有 `agent_task_runs`、`agent_interactive_runs`、workflow 推进、workspace/principal 授权、proposal/approval、tool gateway、Runtime claim/lease/fencing、重试、DLQ 和人工 reconciliation。`domainry-agent` 只执行已授权的 Agent 请求，并返回 provider run identity、标准化结果、错误分类和 usage；provider 完成不等于 Runtime workflow 已提交。

项目组合根通过 `runtimehost.Options.AgentFactory` 选择拓扑。Module Factory 在 Runtime 进程内打开 Binding；SaaS Factory 返回远程 Binding。Runtime 对 nil Binding、未知 mode 和协议不匹配 fail closed，Agent Provider 的地址、凭据和协议配置全部归 `domainry-agent` 所有。

## 公共合同

- SDK：`domainry-agent-sdk/sdk.go`
- 协议：`domainry-agent-protocol-v1`
- 能力：`task.start`、`task.poll`、`task.cancel`、`interactive.run`、`structured_output`、`usage`、`tool_callback`
- Runtime 业务 adapter：`runtime/bootstrap/runtime/agent_sdk_binding.go`
- 稳定错误：`agent.runner.not_configured`、`agent.runner.transport_failed`、`agent.runner.provider_http_<status>`、`agent.runner.response_invalid`

## 数据、事务与 worker

- Module 第一阶段无自有数据库，因此不申请 migration；若以后持久化 provider execution，必须通过 Runtime migration registrar 写入唯一 `_schema_migrations` ledger。
- SaaS provider execution 数据归 Agent SaaS；不得访问 Runtime 数据库。
- Runtime task run 与 provider run 通过稳定 idempotency key 和 `external_run_id` 关联；不共享数据库事务。
- Runtime worker 继续负责业务 task claim、lease、heartbeat、retry、DLQ 和 workflow terminal commit；Agent 只负责 provider execution。
- 不确定 Start 结果必须先 Poll/reconcile，不得直接创建第二个 provider run；Cancel 必须幂等。

## Tool 与安全边界

Agent 只能携带 Runtime 签发的短期 `execution_credential` 调用 Runtime Agent Tool Gateway。Agent SDK/实现不能取得 Runtime service container、repository、数据库或内部授权对象；tool 的授权、risk policy、proposal 和 audit 仍由 Runtime 完成。

## 代码接入

- Runtime 注入：`pkg/runtimehost/options.go`、`pkg/runtimehost/host.go`
- Runtime 打开/关闭：`runtime/bootstrap/runtime/startup.go`、`worker_registry.go`
- SDK adapter：`runtime/bootstrap/runtime/agent_sdk_binding.go`
- Module：`domainry-agent/module`
- SaaS Remote：`domainry-agent/remote`
- SaaS 服务：`domainry-agent/server`、`domainry-agent/cmd/domainry-agent`
- Provider adapter：`domainry-agent/internal/provider`

## 切换与回滚

切换前停止新 task claim，排空或冻结 running provider runs，记录所有 Runtime task run 与 provider run 对照，等待旧 Binding 无未决执行后再切换 Factory。回滚采用同样 fencing；禁止 Module 与 SaaS 同时接受同一 application/idempotency namespace 的 Start。

## 验证

- `domainry-agent-sdk/contracttest` 对 Module/SaaS Binding 执行同一 Descriptor、capability 和 runner 完整性检查。
- `domainry-agent/remote` 通过真实 HTTP test server 验证 Descriptor 握手、鉴权、幂等键和 task 协议。
- Runtime 的 Module-only/SaaS-only 外部项目编译测试均包含 `domainry-agent` 依赖闭包。
- Runtime 旧 `runtime/infrastructure/agentrunner/http` provider adapter 与隐式 runner 选择已删除。
