# Scheduler 模块

状态：Module 与 SaaS 已接入  
Owner：schedule definition/state、run、run event、dead letter、clock worker 与重调度命令  
实现/SDK：`domainry-scheduler` / `domainry-scheduler-sdk`

## 边界与模式

Runtime 通过 `SchedulerFactory` 选择拓扑。Bootstrap 对 Module Factory 调用 `OpenModule`，对 SaaS Factory 调用 `OpenSaaS`。Module 通过 Host 使用 owner migration 和本地数据库，并由 Runtime worker admission 管理启动/关闭；SaaS 自己拥有 clock worker、lease、heartbeat、retry 和 DLQ，Runtime 只发命令/接收结果。

Scheduler definition、state、run、dead-letter、preview 与 operator command API 不再挂载到 Runtime。它们由 `domainry-scheduler` 的 Module/SaaS 边界发布，Runtime 不导入这些 HTTP Action，也不登记对应权限。

Runtime Bootstrap 只把已发布 definition projection 作为 Scheduler `Binding.Reconcile` 的宿主输入；Runtime Application 不再存在 Scheduler service。Scheduler 服务是唯一调度入口；它解析并认领 run 后，用 HMAC 签名把自包含的 target execution（execution id、幂等键、目标、到期时间）提交到 Runtime-owned `POST /dispatch/executions`，Runtime 验签后启动目标。该入口的 `source_owner=dispatch`。Runtime 不回查 Scheduler definition，也不提供 definition、clock、run、retry、cancel 或 DLQ 调度语义。

Runtime Record Timer 是独立能力：Application 位于 `runtime/application/recordtimer`，Domain 位于 `runtime/domain/recordtimer`，配置只使用 `RECORD_TIMER_*`。它不导入 Scheduler，也不复用 Scheduler worker、clock、lease 或 principal。Workflow deadline 仍由 Workflow owner 负责；只有 recurrence/run ownership 明确属于 Scheduler 的状态才归 Scheduler。

Scheduler 下游回调只委托目标 owner。当前直接目标是 scheduled Workflow 与 Report snapshot refresh；Report export 必须经由 scheduled Workflow 和项目 Action 进入 Report 的治理用例。Scheduler 不创建 `report_definition`、query run、export audit、download record/file，也不能自行写 completed/ready 状态。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Bootstrap：`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`、`runtime/bootstrap/runtime/startup.go`
- SDK：`domainry-scheduler-sdk/sdk.go`、`modulehost`、`saashost`
- Authoring/preview：`domainry-scheduler-sdk/authoring`、`domainry-scheduler-sdk/schedule/preview.go`（由 Scheduler owner 暴露）
- Runtime 通用目标执行：`runtime/application/dispatch/target_execution_application_service.go`、`runtime/transport/http/dispatch`
- Module/Remote：`domainry-scheduler/module`、`domainry-scheduler/remote`
- 运维登记：`docs/architecture/runtime-worker-inventory.md`

## 变更约束

- 所有 run 必须有幂等键、claim/fencing、有限 retry、terminal DLQ 与 cancel/recovery 语义。
- Module 与 SaaS 不能同时对同一 schedule owner 启动 clock worker。
- 切换前必须 fence 旧 worker、确认 lease 过期/释放、迁移 cursor/run 状态，再开放新 worker。
