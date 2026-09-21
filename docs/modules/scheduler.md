# Scheduler 模块

状态：Module 与 SaaS 已接入  
Owner：schedule definition/state、run、run event、dead letter、clock worker 与重调度命令  
实现/SDK：`domainry-scheduler` / `domainry-scheduler-sdk`

## 边界与模式

Runtime 通过 `SchedulerFactory` 选择拓扑。Bootstrap 对 Module Factory 调用 `OpenModule`，对 SaaS Factory 调用 `OpenSaaS`。Module 通过 Host 使用 owner migration 和本地数据库，并由 Runtime worker admission 管理启动/关闭；SaaS 自己拥有 clock worker、lease、heartbeat、retry 和 DLQ，Runtime 只发命令/接收结果。

Scheduler definition、state、run、dead-letter、preview 与 operator command API 不再挂载到 Runtime。它们由 `domainry-scheduler` 的 Module/SaaS 边界发布，Runtime 不导入这些 HTTP Action，也不登记对应权限。

Runtime Bootstrap 只把已发布 definition projection 作为 Scheduler `Binding.Reconcile` 的宿主输入；Runtime Application 不再存在 Scheduler service。Scheduler 服务是唯一调度入口；它解析并认领 run 后，用 HMAC 签名把自包含的 target execution（definition key、execution id、幂等键、完整目标、到期时间）提交到 Runtime-owned `POST /dispatch/executions`，Runtime 验签后启动目标。该入口的 `source_owner=dispatch`。Runtime 不回查 Scheduler definition，也不提供 definition、clock、run、retry、cancel 或 DLQ 调度语义。

Runtime Record Timer 是独立能力：Application 位于 `runtime/application/recordtimer`，Domain 位于 `runtime/domain/recordtimer`，配置只使用 `RECORD_TIMER_*`。它不导入 Scheduler，也不复用 Scheduler worker、clock、lease 或 principal。Workflow deadline 仍由 Workflow owner 负责；只有 recurrence/run ownership 明确属于 Scheduler 的状态才归 Scheduler。

Scheduler 下游回调只委托目标 owner。当前直接目标包括 scheduled Workflow、Report snapshot refresh 和 `business_action`。Report export 必须经由 scheduled Workflow 和项目 Action 进入 Report 的治理用例。Scheduler 不创建 `report_definition`、query run、export audit、download record/file，也不能自行写 completed/ready 状态。

`business_action` target 必须同时声明 `target_key`、`target_object`、`run_as_role` 和 JSON object payload。payload 最大 64 KiB，只允许恰好一个 JSON object，并以 `json.Number` 保留大整数精度。发布时 Runtime 校验 Action 与 Object 对应关系、Action 为 object-scoped、角色是 `service` + `system_managed`，且角色拥有目标 Action 的精确权限；任一条件不满足即拒绝发布/启动。每个 definition 被投影为 Identity 管理的 workload binding，执行时按当前 release lineage 解析 principal，不允许退化到 installation system principal。Runtime 仅通过 Action Application Service 执行，沿用 Action 的权限、事务、审计和错误合同，并以 Scheduler window idempotency key 防止同一窗口重试重复提交。暂停、恢复或 schedule 参数变化不会改变授权身份；Action、Object 或角色变化会产生新的授权版本。

`RunBusinessJob` 仍是 Business Handler 在当前事务中登记的一次性 Record Timer 工作，不承担 Cron/日/周周期调度；其 payload 同样是最大 64 KiB 的单一 JSON object，并在 Timer 持久化前校验。周期任务只使用 Scheduler `business_action`，两者不会形成两套周期协议。

## 业务日历合同

Runtime 通过 `schema.business_calendar` 拥有版本化日历定义：稳定 key、revision、IANA timezone、每周工作区间、节假日和按日期覆盖的例外。Scheduler authoring 的 `business_calendar_key` 只适用于 calendar schedule，且必须同时给出与日历一致的 timezone；`non_working_day_policy` 只能是 `skip` 或 `roll_forward`。固定 interval 不接受业务日历，因为 interval 表达的是经过时长，不是本地工作日。

Runtime 在定义投影时解析引用并嵌入完整 `BusinessCalendarSnapshot`，同时把 key、revision 和快照摘要计入 Scheduler definition revision。Module 与 SaaS 因而消费同一份不可变协议，不回调 Runtime 查询当前日历，也不执行项目提供的日期函数。`skip` 在关闭日期不创建 run；`roll_forward` 把 occurrence 推到下一个开放本地日期。Preview、misfire 与实际 clock 使用同一 Scheduler SDK 语义。

Workflow 的日历语义与 Scheduler 不同：Workflow 是“从基准时间增加工作时长”，并把日历 revision 固化到 Timer；Scheduler 是“周期 occurrence 遇到非工作日时跳过或顺延”。两者复用同一个源定义，但不能互相替代。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Bootstrap：`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`、`runtime/bootstrap/runtime/startup.go`
- SDK：`domainry-scheduler-sdk/sdk.go`、`modulehost`、`saashost`
- Authoring/preview：`domainry-scheduler-sdk/authoring`、`domainry-scheduler-sdk/schedule/preview.go`（由 Scheduler owner 暴露）
- Runtime 通用目标执行：`runtime/application/dispatch/target_execution_application_service.go`、`runtime/transport/http/dispatch`
- Business Action 与受管 workload：`runtime/bootstrap/composition/scheduler_business_action_wiring.go`、`runtime/application/workflow/managed_workload_principal_application_service.go`
- 业务日历协议：`runtime/domain/businesscalendar`、`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`、`domainry-scheduler-sdk/schedule/business_calendar.go`
- Module/Remote：`domainry-scheduler/module`、`domainry-scheduler/remote`
- 运维登记：`docs/architecture/runtime-worker-inventory.md`

## 变更约束

- 所有 run 必须有幂等键、claim/fencing、有限 retry、terminal DLQ 与 cancel/recovery 语义。
- `business_action` 的 definition key、Action、Object、service role 和 payload 必须完整进入 Module 与 SaaS 的签名 dispatch 合同，任何 adapter 都不能丢字段。
- Scheduler 不能直接调用项目 Handler，也不能以 system principal 补偿缺失的 service-role 权限。
- Module 与 SaaS 不能同时对同一 schedule owner 启动 clock worker。
- 切换前必须 fence 旧 worker、确认 lease 过期/释放、迁移 cursor/run 状态，再开放新 worker。
