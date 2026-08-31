# Monitoring 模块

状态：Module 与 SaaS 已接入  
Owner：业务监控记录、指标/状态查询及 Monitoring 自有生命周期  
实现/SDK：`domainry-monitoring` / `domainry-monitoring-sdk`

## 边界与模式

Runtime 通过 `MonitoringFactory` 注入拓扑。`runtime/bootstrap/runtime/monitoring_module_host.go` 根据 Factory 支持的接口调用 `OpenModule` 或 `OpenSaaS`，并校验返回 Binding/Descriptor。Module 借用受限 Host；SaaS 使用远程服务。Monitoring 是业务能力合同，不替代 Runtime 进程自身的 OpenTelemetry/logging。

产品 HTTP `GET /operations/monitoring/metrics` 由 Monitoring Binding 的 `modulehttp.Surface` 声明和实现。Runtime 只负责通用 Surface 挂载、宿主身份鉴权、监听器隔离及从 Surface 声明生成 OpenAPI；Runtime 不保留 Monitoring handler、静态 endpoint inventory 或静态 OpenAPI 路径。

Runtime 应用层只提供 owner-produced observations、storage/migration readiness 与 migration telemetry。健康判级、warning 规则、Monitoring envelope、Runtime/模板身份装配均由 Monitoring 实现；Runtime 不提供后备 `Health`/`Metrics` 聚合实现，避免 Module/SaaS 与后备路径产生语义漂移。

Runtime 领域层的 workflow、business action、audit、record 和 idempotency 指标投影仍属于各数据 owner 的观测语义，并通过 Host 端口提供给 Monitoring。它们依赖 Runtime/owner 模型，不下沉到 Monitoring；后续若对应 owner 模块提供原生 observation port，应迁回该 owner，而不是让 Monitoring 依赖 Runtime 领域模型。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Host：`runtime/bootstrap/runtime/monitoring_module_host.go`
- HTTP：`domainry-monitoring/internal/transport/http/module/surface.go`
- SDK：`domainry-monitoring-sdk/sdk.go`、`modulehost`、`saashost`、`remote`
- 实现：`domainry-monitoring/module` 与 SaaS 组装/服务入口

## 变更约束

- 先区分产品 Monitoring 数据与平台 telemetry，避免双重 owner。
- Remote 不可用时的 readiness、缓冲、丢弃或拒绝策略必须按操作类型明确，禁止静默成功。
- 两种模式的 workspace、时间范围、聚合与保留语义必须由 contract test 对齐。
