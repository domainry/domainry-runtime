# Monitoring 模块

状态：Module 与 SaaS 已接入  
Owner：业务监控记录、指标/状态查询及 Monitoring 自有生命周期  
实现/SDK：`domainry-monitoring` / `domainry-monitoring-sdk`

## 边界与模式

Runtime 通过 `MonitoringFactory` 注入拓扑。`runtime/bootstrap/runtime/monitoring_module_host.go` 根据 Factory 支持的接口调用 `OpenModule` 或 `OpenSaaS`，并校验返回 Binding/Descriptor。Module 借用受限 Host；SaaS 使用远程服务。Monitoring 是业务能力合同，不替代 Runtime 进程自身的 OpenTelemetry/logging。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Host：`runtime/bootstrap/runtime/monitoring_module_host.go`
- SDK：`domainry-monitoring-sdk/sdk.go`、`modulehost`、`saashost`、`remote`
- 实现：`domainry-monitoring/module` 与 SaaS 组装/服务入口

## 变更约束

- 先区分产品 Monitoring 数据与平台 telemetry，避免双重 owner。
- Remote 不可用时的 readiness、缓冲、丢弃或拒绝策略必须按操作类型明确，禁止静默成功。
- 两种模式的 workspace、时间范围、聚合与保留语义必须由 contract test 对齐。

