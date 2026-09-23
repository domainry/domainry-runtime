# Agent 模块

状态：SDK、Module、SaaS Binding、产品 HTTP Adapter 与独立 SaaS 服务入口已接入
Owner：Agent/Skill/Task 定义、会话与 proposal、interactive/task 执行、provider run、tool-call ledger、worker/retry/reconcile
实现/SDK：`domainry-agent` / `domainry-agent-sdk`

## 所有权边界

`domainry-agent` 拥有 Agent 产品用例和状态，包括 session/dialog、proposal 决策、interactive run、task run、approval lifecycle、provider correlation、claim/lease/fencing、tool-call ledger、重试和诊断。Agent/Skill/Task/Context/Routing/Runner 公共合同只在 `domainry-agent-sdk` 声明。

Runtime 不再拥有 `agent_dialog` Handler、Agent Application Service、Agent Domain Model 或 Agent Repository。Runtime 只保留：

- Workflow 的 process/node/execution 状态与推进；
- 当前 workspace/principal/permission 解析；
- Record、Action、Workflow、Report 等宿主业务能力和高风险策略；
- Runtime audit 持久化；
- 供 Agent 调用的窄化 Host Ports 与 workflow task 完成回调；
- Agent Binding、HTTP Adapter 和宿主 middleware 的组合。

项目组合根通过 `runtimehost.Options.AgentFactory` 选择 Module 或 SaaS。两种拓扑必须提供相同 SDK 能力和同一 Agent-owned HTTP Adapter；Runtime 只负责挂载 Adapter，不复制路径、Handler 或 API 描述表。

## 调用方向与异步边界

Workflow Agent 节点按以下时序执行：

1. Runtime 为 `process_id + node_id + iteration` 生成确定性的 `node_instance_id` 和 `agent_task_run_id`。
2. Runtime 调用 `TaskRunner.Start`。Agent 只需持久化 task 并返回 `accepted`。
3. Runtime 提交 Workflow node 的 `waiting` 状态、相关事件和 Agent task correlation；Runtime 事务不写 Agent 表。
4. Agent worker 在自己的调用栈中 claim/lease、调用 provider，并按需通过 `TaskHost` 请求 Runtime 的当前授权、凭据或具体 business tool effect。
5. Agent 先提交自身 terminal 状态，再以 `TaskHost.CompleteWorkflowTask` 异步通知 Runtime。
6. Runtime 校验 workspace/process/node/task-run correlation，只提交 Workflow 状态并唤醒 continuation；它不会在该回调中再次调用 Agent。
7. Runtime 接收成功后，Agent 标记 workflow completion 已送达；失败则由 Agent reconcile 重试同一个幂等回调。

因此模块间存在两条业务依赖边，但不存在同步循环：Workflow dispatch 的 Runtime→Agent 调用在 `accepted` 处结束；Agent→Runtime Host 调用只发生在后续 Agent worker 或独立 HTTP 请求中。禁止恢复 Runtime→Agent→Runtime 的同栈重入，也禁止用跨 owner 数据库事务模拟原子性。

## 公共合同与 HTTP

- SDK：`domainry-agent-sdk/sdk.go`
- Host Ports：`domainry-agent-sdk/modulehost/application.go`
- Agent 定义校验：`domainry-agent/definition/validation.go`
- 协议：`domainry-agent-protocol-v1`
- 执行能力：`task.start`、`task.poll`、`task.cancel`、`interactive.run`、`dialog.state`、`execution.state`、`structured_output`、`usage`、`tool_callback`
- Agent 产品 HTTP Adapter：统一位于 `/agent/*`，覆盖 run、stream、session、proposal、task-run、task-tool、analysis、diagnostics 和 task operator mutation。
- Agent Adapter 自己声明全部 typed route metadata；Agent 当前 SDK 源码和契约测试定义精确接口。Runtime 不声明 Agent 路径或 `agent_*` 文档 schema。
- SaaS 内部协议使用固定的 definition、lifecycle 和 execution-state 路径，不暴露通用 repository operation 或 task mutation endpoint。

## 数据、事务与 worker

- Module 借用宿主数据库、SQL 方言、迁移锁和唯一 `_schema_migrations`；Agent Factory 以 owner `agent` 注册 source-owned migration。
- SaaS 使用独立 Agent 数据库；它不得访问 Runtime 数据库。
- Agent task 的创建和全部状态迁移都在 Agent Repository 中提交；Runtime workflow 事务只提交 Workflow 状态。
- 已删除旧 `AgentTaskMutation`、`AgentTaskTransactionRepository`、`_agent_task_publications` outbox 和远端 mutation route。跨 owner 一致性由确定性 identity、幂等 Start、terminal-first callback 和 reconcile 达成。
- Agent worker 属于 Agent Binding：负责 claim、lease、heartbeat、provider poll/start/cancel、retry/dead-letter、approval、tool evidence 和 callback reconcile。Runtime 不启动或实现第二套 Agent worker/state machine。
- 不确定 Start 结果必须 Poll/reconcile，不得直接创建第二个 provider run；Cancel 和 completion callback 必须幂等。

## Tool 与安全边界

Agent 决定对话、proposal、approval lifecycle、允许的 Agent tool 次数/成本和 tool-call ledger。Runtime Host Port 在每次 effect 前重新解析当前 principal/permission，并执行 Runtime-owned input/output/rate/risk policy。

Agent 只能通过 `InteractiveHost`、`TaskHost`、`ProposalHost`、`AuditHost` 和 `AnalysisHost` 请求宿主事实或效果；SDK/实现不能取得 Runtime service container，也不能 import Runtime implementation package。Host Port 参数只携带稳定引用、当前请求和授权证据，不能传入完整 Agent aggregate。

## 代码接入

- Runtime 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/host.go`
- Runtime 打开 Binding：`runtime/bootstrap/runtime/agent_sdk_binding.go`
- Runtime Host adapter：`runtime/application/agenthost`、`runtime/bootstrap/transport/http_integration_agent_handler_wiring.go`
- Runtime Workflow dispatch/completion：`runtime/application/workflow/workflow_process_runtime_application_service.go`、`workflow_agent_capability_completion.go`
- Agent Module：`domainry-agent/module`
- Agent SaaS Remote/Server：`domainry-agent/remote`、`domainry-agent/server`
- Agent 产品 Adapter：`domainry-agent/internal/transport/http/module`
- Agent Application：`domainry-agent/internal/application`

## 验证约束

- Runtime 生产代码不得出现 Agent aggregate/repository/state-machine 实现，也不得自行声明 `/agent/*` 路由。
- Agent 不得 import `domainry-runtime`。
- Runtime 的 Agent capability contract 不得再包含 Agent 产品路径或 `agent_*` 文档 schema。
- Module/SaaS 必须执行相同 Agent contract tests，并验证 Start 幂等、fencing、terminal callback 重放和 HTTP route parity。
- 切换拓扑前必须停止新 claim 并排空或冻结 running provider runs；禁止 Module 与 SaaS 同时接受同一 application/idempotency namespace。
