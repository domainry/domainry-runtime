# Scheduler 模块

状态：Module 与 SaaS 已接入  
Owner：schedule definition/state、run、run event、dead letter、clock worker 与重调度命令  
实现/SDK：`domainry-scheduler` / `domainry-scheduler-sdk`

## 边界与模式

Runtime 通过 `SchedulerFactory` 选择拓扑。Bootstrap 对 Module Factory 调用 `OpenModule`，对 SaaS Factory 调用 `OpenSaaS`。Module 通过 Host 使用 owner migration 和本地数据库，并由 Runtime worker admission 管理启动/关闭；SaaS 自己拥有 clock worker、lease、heartbeat、retry 和 DLQ，Runtime 只发命令/接收结果。

Runtime 的 `record_timer`、workflow deadline 等 owner-specific timer 不能因 Scheduler 存在就无条件迁入；只有 recurrence/run ownership 明确属于 Scheduler 的状态才归 Scheduler。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Bootstrap：`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`、`runtime/bootstrap/runtime/startup.go`
- SDK：`domainry-scheduler-sdk/sdk.go`、`modulehost`、`saashost`
- Module/Remote：`domainry-scheduler/module`、`domainry-scheduler/remote`
- 运维登记：`docs/architecture/runtime-worker-inventory.md`

## 变更约束

- 所有 run 必须有幂等键、claim/fencing、有限 retry、terminal DLQ 与 cancel/recovery 语义。
- Module 与 SaaS 不能同时对同一 schedule owner 启动 clock worker。
- 切换前必须 fence 旧 worker、确认 lease 过期/释放、迁移 cursor/run 状态，再开放新 worker。

