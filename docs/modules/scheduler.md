# Scheduler 模块

状态：Module 与 SaaS 已接入  
Owner：schedule definition/state、run、run event、dead letter、clock worker 与重调度命令  
实现/SDK：`domainry-scheduler` / `domainry-scheduler-sdk`

## 边界与模式

Runtime 通过 `SchedulerFactory` 选择拓扑。Bootstrap 对 Module Factory 调用 `OpenModule`，对 SaaS Factory 调用 `OpenSaaS`。Module 通过 Host 使用 owner migration 和本地数据库，并由 Runtime worker admission 管理启动/关闭；SaaS 自己拥有 clock worker、lease、heartbeat、retry 和 DLQ，Runtime 只发命令/接收结果。

`GET /operations/scheduler/state` 通过 SDK `Binding.Runs` 与 `Binding.DeadLetters` 读取 owner 状态。Tenant Admin DTO、definition projection、authoring capability schema/example 和 preview 规则由 Scheduler SDK 提供；Runtime Application 只做权限检查、源控定义读取和聚合适配，不再声明 run/dead-letter 状态模型，也不落一份镜像 Record。

当前 Scheduler HTTP 路由仍挂在 Runtime transport：这些 handler 同时依赖 Runtime Principal、源控定义读取、Operations 幂等回执和 dispatch-gateway 鉴权，属于宿主协议装配。把它们直接搬进 Scheduler 会迫使 owner 反向依赖 Runtime 类型；在 SDK 尚未定义完整的 HTTP host ports 前不做这种伪下沉。路由消费的 DTO、Schema、示例、preview 与 owner command 均已由 Scheduler SDK/Binding 提供。

Runtime Record Timer 是独立能力：Application 位于 `runtime/application/recordtimer`，Domain 位于 `runtime/domain/recordtimer`，配置只使用 `RECORD_TIMER_*`。它不导入 Scheduler，也不复用 Scheduler worker、clock、lease 或 principal。Workflow deadline 仍由 Workflow owner 负责；只有 recurrence/run ownership 明确属于 Scheduler 的状态才归 Scheduler。

Scheduler 下游回调只委托目标 owner。当前直接目标是 scheduled Workflow 与 Report snapshot refresh；Report export 必须经由 scheduled Workflow 和项目 Action 进入 Report 的治理用例。Scheduler 不创建 `report_definition`、query run、export audit、download record/file，也不能自行写 completed/ready 状态。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Bootstrap：`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`、`runtime/bootstrap/runtime/startup.go`
- SDK：`domainry-scheduler-sdk/sdk.go`、`modulehost`、`saashost`
- Authoring/preview：`domainry-scheduler-sdk/authoring`、`domainry-scheduler-sdk/schedule/preview.go`
- Runtime 下游宿主适配：`runtime/modulehost/scheduler/downstream_dispatcher.go`
- Module/Remote：`domainry-scheduler/module`、`domainry-scheduler/remote`
- 运维登记：`docs/architecture/runtime-worker-inventory.md`

## 变更约束

- 所有 run 必须有幂等键、claim/fencing、有限 retry、terminal DLQ 与 cancel/recovery 语义。
- Module 与 SaaS 不能同时对同一 schedule owner 启动 clock worker。
- 切换前必须 fence 旧 worker、确认 lease 过期/释放、迁移 cursor/run 状态，再开放新 worker。
