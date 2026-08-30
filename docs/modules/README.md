# 外部能力模块索引

本目录记录 Runtime 之外的独立能力 owner。总规则见 [`../architecture/module-saas-development-standard.md`](../architecture/module-saas-development-standard.md)。每次能力合同、数据所有权、ModuleHost、部署模式或切换流程变化，必须在同一 PR 更新对应文档。

| 能力 | Runtime 注入点 | Module | SaaS | 当前说明 |
| --- | --- | --- | --- | --- |
| [Identity](identity.md) | `runtimehost.Options.IdentityFactory` | 已实现 | 已实现 | Module 独有进程内 HTTP Surface |
| [Notification](notification.md) | `runtimehost.Options.NotificationFactory` | 已实现 | 已实现 | SaaS publication outbox 已在 Runtime 清单登记 |
| [Party](party.md) | `runtimehost.Options.PartyFactory` | 已实现 | 已实现 | 组织范围通过 SDK 投影给 Identity |
| [Monitoring](monitoring.md) | `runtimehost.Options.MonitoringFactory` | 已实现 | 已实现 | Module/SaaS Host 分支已存在 |
| [Scheduler](scheduler.md) | `runtimehost.Options.SchedulerFactory` | 已实现 | 已实现 | Module worker 受 Runtime admission，SaaS 拥有 clock worker |
| [Agent](agent.md) | `runtimehost.Options.AgentFactory` | 已实现 | 已实现 | Runtime 保留 task/workflow/授权 owner；Agent 拥有 provider execution |
| [Data Exchange](data-exchange.md) | `runtimehost.Options.DataExchangeFactory` | 已实现 | 已实现 | Host 暴露 import/export provider |
| [Integration](integration.md) | `runtimehost.Options.IntegrationFactory` | 已实现 | 已实现 | Runtime 只保留 durable publication handoff |
| [Metadata](metadata.md) | Runtime bootstrap 固定组装 | 已实现 | 未形成可选拓扑 | 定义版本与 workspace preference/rule-set 归 Metadata |
| [Lifecycle](lifecycle.md) | Runtime bootstrap 固定组装 | 已实现 | 未形成可选拓扑 | Runtime 保留运维 HTTP 编排，状态与 persistence 归 Lifecycle |
| [Report](report.md) | Runtime bootstrap 固定组装 | 已实现 | 未形成可选拓扑 | Runtime 同步 manifest 定义，执行状态归 Report |
| [Audit](audit.md) | Runtime bootstrap 固定组装 | 已实现 | 未形成可选拓扑 | 支持同事务 prepared append |

## 新模块文档要求

新模块复制 [`_template.md`](_template.md) 并填写代码证据。禁止只登记计划而不区分“已实现”和“目标状态”。
