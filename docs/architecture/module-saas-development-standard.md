# Domainry Module / SaaS 拆分与开发规范

状态：当前架构规范（2026-08-30）  
适用范围：Domainry Runtime、独立能力仓库、对应 SDK、生成的项目组合代码

## 1. 目的与权威边界

本规范把仓库中已经落地的 Module / SaaS 双形态方案固化为后续拆分标准。它回答四个问题：什么时候应该拆、拆成哪些仓库、Runtime 如何接入、完成拆分必须通过哪些验收。

本规范补充 [`backend-development-guide.md`](backend-development-guide.md)，不替代其中的 Runtime 分层和依赖规则。若两者冲突：Runtime 内部代码按后端开发指导执行；跨仓能力、SDK 和部署拓扑按本规范执行。

## 2. 当前代码证明的架构

当前项目已经形成以下稳定边界：

- `pkg/runtimehost.Options` 只接收 Identity、Notification、Party、Monitoring、Scheduler、Data Exchange 的 SDK `Factory`，不接收这些能力的内部 service/store。
- `pkg/runtimehost/external_module_test.go` 分别编译纯 Module 组合与 SaaS Remote 组合，证明项目代码只需更换 Factory，不需要更改 Runtime 业务代码。
- `runtime/bootstrap/runtime/*_module_host.go` 提供 Runtime 授权给 Module 的最小 Host 能力，并依据 Factory 类型调用 `OpenModule` 或 `OpenSaaS`。
- `runtime/infrastructure/persistence/database/module_migrations.go` 统一执行 owner-qualified migration、checksum、dirty state、全局迁移锁和 RLS 复核。
- `pkg/runtimehost/identity_integration.go` 额外约束 Identity：Module 必须返回进程内 HTTP Surface；SaaS 不得返回进程内 Surface。
- `runtime/infrastructure/persistence/auditmodule` 证明 Audit 已是独立源码 owner，但当前由 Runtime 固定以内嵌 Module 打开，尚不是可选 SaaS 拓扑。

因此，Domainry 的方案不是“复制两套实现”，而是：

```text
业务调用方 / Runtime
        |
        v
   <capability>-sdk          稳定合同、DTO、错误、Descriptor、Binding
        |
        +---------------------------+
        |                           |
        v                           v
<capability>/module          <capability>/remote
进程内实现                    SaaS transport adapter
        |                           |
        v                           v
Runtime ModuleHost            远程 SaaS 服务
数据库/迁移/回调/时钟          独立数据、worker、扩缩容
```

## 3. 三种“模块”不得混淆

| 类型 | 代码位置 | 含义 | 是否独立部署 |
| --- | --- | --- | --- |
| Runtime 领域模块 | `runtime/domain/<owner>` | Runtime 内的业务 owner，按 DDD 和分层拆包 | 否 |
| 外部 Module | `domainry-<capability>/module` | 通过 SDK 嵌入项目 Runtime 进程的独立源码 owner | 否，随 Runtime 进程部署 |
| SaaS | 独立服务 + SDK Remote adapter | 同一能力合同的远程部署形态 | 是 |

仅仅把代码移动到新目录不叫外部 Module。外部 Module 必须拥有独立 Go module、独立 SDK 合同、独立状态所有权和可独立测试的依赖闭包。

## 4. 拆分判定

满足以下条件时才进入外部能力拆分：

1. 能力有明确且稳定的业务 owner，不是 `common`、`utils`、`shared` 一类技术集合。
2. 能力能用业务命令/查询描述，不要求调用方取得其内部 entity、repository 或数据库表。
3. 状态、迁移、worker、重试、保留策略和故障责任可以归属给该 owner。
4. Runtime 只需要一个窄 Host 端口即可支持本地 Module。
5. 远程化后可以保持同一语义合同；超时、重试和最终一致性差异能够显式建模。
6. 拆分不会让 SDK 依赖实现仓库，也不会让能力的 domain/application 反向依赖 Runtime implementation。

以下情况保留在 Runtime 领域模块：需要参与 Runtime 同一聚合事务、规则仍快速变化且边界未稳定、拆出后只能暴露数据库或内部 service、或只是为减少文件数量而拆包。

## 5. 标准仓库与目录

### 5.1 SDK 仓库 `domainry-<capability>-sdk`

SDK 是唯一允许调用方依赖的业务合同。推荐结构：

```text
domainry-<capability>-sdk/
  sdk.go                    # ApplicationRef、Descriptor、Factory、Binding
  contract/                 # 稳定业务 DTO/Value Object
  modulehost/               # Module 所需的最小 Host port
  saashost/                 # Remote transport/factory port（需要时）
  remote/                   # 纯 SDK remote client（适合放在 SDK 时）
  contracttest/             # Module 与 SaaS 共用验收套件
```

硬约束：

- SDK 不得 import 能力实现仓库或 Runtime implementation。
- 公共合同不得泄漏 `database/sql`、HTTP handler、ORM model、内部配置或实现错误。
- `Descriptor` 至少声明协议版本与 `module|saas` 模式；能力有版本、audience、capabilities 时一并校验。
- `Binding.Close(ctx)` 归还生命周期；可选能力用窄接口表达，不通过类型字段泄漏实现。
- 错误必须具有稳定 code/kind，SaaS transport error 需转换为与 Module 一致的业务语义。

### 5.2 实现仓库 `domainry-<capability>`

推荐结构：

```text
domainry-<capability>/
  internal/domain/<owner>/
  internal/application/
  internal/infrastructure/
  internal/transport/
  internal/assembly/module/
  internal/assembly/saas/
  module/                   # 薄公共 facade
  remote/                   # SaaS Binding adapter（若不在 SDK）
  cmd/<capability>-server/  # SaaS 进程入口
```

`module/` 必须是薄组合边界，不能成为第二套 domain/application。Module 与 SaaS 应复用同一业务内核，只在 assembly、persistence host、transport 和 worker ownership 上不同。

### 5.3 Runtime 仓库

Runtime 只保留：

- `pkg/runtimehost.Options` 中的 SDK Factory 注入点；
- `runtime/bootstrap/runtime/<capability>_module_host.go` 中的 Host adapter 与 Binding 组装；
- 必要的 application facade 或 domain contract adapter；
- `runtime/infrastructure/.../<capability>module` 中对 Runtime port 的适配（确有需要时）；
- composition、contract、integration 和外部编译测试。

Runtime 不保留外部能力的规则、表模型、迁移 SQL、远程协议实现或 worker 状态机。

## 6. Module 与 SaaS 的统一合同

### 6.1 组合根决定拓扑

生成的项目 composition 必须显式注入 Module Factory 或 Remote Factory。Runtime 禁止读取环境变量后自行切换拓扑，也禁止运行时 fallback：

```go
runtimehost.Options{
    NotificationFactory: notificationmodule.NewFactory(...), // Module
}

runtimehost.Options{
    NotificationFactory: notificationmodule.NewSaaSFactory(
        notificationremote.NewFactory(...),
    ), // SaaS remote Binding + Notification-owned product HTTP Surface
}
```

这是构建身份和可重复部署的一部分。拓扑变更必须重新生成、验证和打包项目。

### 6.2 Factory / Binding 生命周期

标准启动顺序：

1. 组合根构造 Factory。
2. Runtime 打开并验证项目数据库、manifest 与 release identity。
3. Bootstrap 构造 capability-specific Host。
4. Module Factory 执行 owner migration 后打开本地 Binding；SaaS Factory 校验远端 Descriptor 后打开 Remote Binding。
5. Runtime 只通过 SDK Binding 调用能力。
6. worker 在 Binding 就绪后启动；shutdown 时先停止接流/worker，再 `Binding.Close(ctx)`。

任何 `nil Binding`、未知 mode、协议不匹配、audience 不匹配、能力缺失都必须 fail closed。

### 6.3 Host 最小授权

Host interface 只暴露该能力真实需要的端口，例如数据库 handle、Dialect、MigrationRegistrar、WorkspaceContext、回调或 provider registry。禁止传递完整 `Runtime`、service container、全局 config 或任意数据库访问器。

新增 Host 方法必须回答：谁授权、用于哪个用例、Module 是否必需、SaaS 是否需要、如何测试 workspace/transaction 边界。

## 7. 数据、事务与迁移

- 每张表只有一个 owner。借用 Runtime 数据库不改变表的所有权。
- Module migration 必须经 Runtime `ApplyOwnedMigrations`，owner 名稳定，版本严格递增，checksum 不可漂移。
- Module 表必须进入 workspace scope、RLS、备份、清理和容量清单；SaaS 表不得伪装成 Runtime 表。
- Runtime 与 Module 需要原子提交时，只能使用 SDK 明确定义的 transaction contract；不能传递裸 `*sql.Tx` 到业务层。Audit 的 prepared append 是当前特例的合同化实现。
- Runtime 与 SaaS 默认不存在数据库事务。使用 outbox、幂等键、receipt、重试和 reconciliation 明确一致性。
- 从 Module 切到 SaaS 必须有数据迁移、双写/停写策略、校验、切流和回滚证据；不能只替换 Factory。

## 8. HTTP、worker 与可观测性

- 产品 HTTP Surface 由能力实现仓通过 Foundation `modulehttp` 合同声明，不能塞进业务 SDK。Module Binding 直接暴露本地 Surface；需要保持同源产品路由的 SaaS 组合，由能力仓的薄 SaaS Factory 在 Remote Binding 外装配同一个 Surface。Remote client 本身不得实现产品 Handler。
- Runtime Host 只校验并挂载 route、exposure、contract version、认证 guard 与重复 owner，不得复制能力产品 Handler。真正跨 Runtime 资源授权或宿主持久化的少量端点可以留在 Runtime，但必须逐条说明 owner。
- Module worker 受 Runtime admission、context cancellation 和 shutdown 管理；SaaS worker 由 SaaS owner 管理。
- 两种模式必须保留相同的业务幂等、重试上限、dead-letter 和取消语义，但 lease、heartbeat 和扩缩容实现可不同。
- 日志、metric、trace 必须携带 capability、mode、workspace/application、operation 和稳定错误码；不得记录 credential 或敏感 payload。
- readiness 必须反映 Binding/协议/远端依赖状态，不能在能力不可用时静默降级。

## 9. 依赖规则

允许：

```text
project composition -> runtimehost + capability implementation/remote
runtimehost/bootstrap -> capability-sdk
capability implementation -> capability-sdk + foundation/orm
capability remote -> capability-sdk
```

禁止：

```text
capability-sdk -> capability implementation
capability domain/application -> runtime/bootstrap|infrastructure|transport
runtime domain -> capability implementation
module implementation -> another module implementation
remote adapter -> module package
```

跨能力协作通过 SDK contract、Runtime application orchestration 或明确事件完成，不能直接 import 另一个能力的内部 service。

## 10. 新能力拆分开发流程

1. 在 `docs/modules/<capability>.md` 登记 owner、能力、状态、数据、worker、Module/SaaS 差异和切换方案。
2. 盘点 Runtime 现有入口、调用者、表、迁移、worker、HTTP、配置、错误和测试。
3. 先提取 SDK 业务合同与 contract test，保持旧实现仍可工作。
4. 将实现移入独立仓库的 domain/application/infrastructure，建立薄 `module/` facade。
5. 在 Runtime 建立最小 ModuleHost 和 composition seam，删除对实现包的直接依赖。
6. 建立 Remote Binding 与 SaaS server；复用同一 contract test。
7. 增加 Module-only、SaaS-only 外部项目编译测试，证明依赖闭包没有带入另一种实现。
8. 完成数据迁移、worker ownership、运行清单和 rollback 演练。
9. 删除 Runtime 中的重复规则/表/worker；更新本规范的模块索引。

## 11. Definition of Done

- [ ] SDK 可在仓库外独立编译，且不下载实现模块。
- [ ] Module 与 SaaS 通过同一 contract test；Descriptor/mode/version 校验为 fail closed。
- [ ] 项目组合根可分别编译 Module-only 和 SaaS-only 依赖闭包。
- [ ] Runtime 仅依赖 SDK，除 composition/明确 adapter 外不 import 实现仓库。
- [ ] owner 表、migration、workspace/RLS、retention、backup、erase 已登记。
- [ ] transaction 或 outbox/idempotency/reconciliation 方案已验证。
- [ ] worker 的 claim、lease、heartbeat、retry、DLQ、cancel、recovery 已登记。
- [ ] startup、readiness、shutdown、Binding close 和故障路径有测试。
- [ ] Module → SaaS 以及 SaaS → Module 的切换/回滚步骤有证据。
- [ ] `docs/modules/<capability>.md` 与真实代码同步。

## 12. 代码审查问题

每个相关 PR 必须回答：

1. 这次修改改变了哪个 owner 的合同或状态？
2. Module 与 SaaS 的业务语义是否仍一致？
3. 是否新增了 SDK 泄漏、Runtime implementation 依赖或隐式 topology switch？
4. 数据与 worker 最终由谁负责？失败后由谁恢复？
5. 哪个 contract/integration/external compile test 证明边界没有退化？
