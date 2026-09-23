# Domainry Runtime 与子模块能力、开发对比和适用场景

本文从普通业务系统开发的视角说明 Domainry Runtime 和外部能力模块分别提供什么、为什么这样设计、普通开发达到同等保障需要付出什么工程代价，以及对应能力的适用边界。

本文是能力理解和架构选型指南，不替代具体合同：

- Runtime 的强制开发边界以 [`backend-development-guide.md`](backend-development-guide.md) 为准；
- Module/SaaS 所有权和一致性以 [`module-saas-development-standard.md`](module-saas-development-standard.md) 为准；
- 各模块当前接入状态以 [`../modules`](../modules/README.md) 和对应 source owner 的可执行合同为准；
- 本文中的“普通开发”是常见项目写法，不表示所有传统项目都存在相同问题。

## 1. 一句话定位

Domainry Runtime 不是把数据库表简单暴露成 CRUD API，也不是代替开发者编写所有业务逻辑。它是一套声明驱动的业务执行底座：

```text
项目业务定义与 Action Handler
              ↓
Runtime：授权、校验、事务、Mutation、Workflow、Automation、运维治理
              ↓
Identity / Notification / Integration / Report / Agent 等专业模块
              ↓
数据库、外部 Provider、文件与独立 SaaS 服务
```

普通开发者仍然负责业务对象、业务动作、业务规则和产品取舍；Runtime 负责让所有业务入口以一致、安全、可恢复、可审计的方式执行。

## 2. 用户能感知的产品能力与系统兜底

### 2.1 最终用户能感知到什么

下面的“用户可感知能力”表示 Runtime 和模块已经提供相应的业务合同、执行能力和产品 API，项目可以据此交付前端体验；它不表示所有项目会自动出现完全相同的页面。具体菜单、页面布局和交互仍由项目前端决定。

| 用户可感知能力 | 用户能用通俗语言理解的产品体验 | 背后的框架能力 |
| --- | --- | --- |
| 统一业务数据 | 能查看、搜索、筛选、新增和维护客户、订单、合同等业务数据，列表和详情行为一致 | Object、Record、字段校验、关系、生命周期、通用 CRUD |
| 明确的业务操作 | 看到“提交审批”“取消订单”“确认收款”等有业务含义的按钮，并获得稳定的成功或失败结果 | Action 合同、前置条件、并发控制、幂等、最小 capability |
| 待办与审批中心 | 查看我的待办和团队待办，批准、拒绝、退回、撤回，并看到流程进度、提醒和升级 | Workflow、Approval、Timer、Process/Task/Event 状态 |
| 自动处理 | 当订单、客户或合同满足条件时，系统自动更新数据、创建后续任务或发送提醒 | Automation、生命周期事件、durable execution |
| 定时执行 | 使用日结、月结、周期同步、报表刷新，并查看执行历史、失败和重试结果 | Scheduler、Run、Lease、Fencing、Retry、DLQ |
| 按职责查看数据 | 普通员工只看自己的数据，主管看团队数据，敏感字段被隐藏或脱敏，高风险操作需要再次验证或审批 | Identity、Principal、Workspace、行级/字段级权限、Assurance |
| 消息与提醒 | 收到站内消息或外部渠道提醒，管理已读、归档、代理和通知偏好 | Notification、Inbox、Template、Preference、Delivery plan |
| 报表与大文件 | 按权限查看分组统计和快照；大批量导入导出时能看进度、取消任务并下载结果 | Report、Data Exchange、稳定分页、Artifact、Job/Chunk |
| 外部系统连接 | 连接 ERP、支付、短信或其他供应商，查看同步和调用结果，失败后可以安全重试 | Integration、Connection、Credential、Connector、Invocation、Receipt |
| AI 辅助业务 | 用自然语言查询和分析业务、生成操作建议；涉及真实业务修改时仍受权限和人工批准控制 | Agent、Skill、Proposal、Tool ledger、Runtime effect re-authorization |
| 操作历史与合规 | 查询谁在什么时候执行了什么操作，处理保留、法律冻结和主体数据请求 | Audit、Lifecycle、不可变事件、Retention、Legal hold |
| 可恢复的系统 | 失败任务不会只剩一行日志；管理员能看到积压、失败原因、重试和人工处理入口 | Monitoring、Operations、Idempotency Receipt、DLQ、Diagnostics |

从用户视角看，框架的价值不是“后端少写了几个 Controller”，而是业务操作更一致，权限更可信，长流程和异步任务有进度，失败后可以恢复，关键操作可以追踪。

### 2.2 代表性例子：定义作者写 Object SQL，框架自动守住数据边界

Object SQL 最能体现 Domainry 的系统能力。报表定义作者可以用熟悉的 SQL 表达聚合逻辑，例如：

```sql
SELECT s.employee_id AS employee_id,
       SUM(s.net_total) AS revenue
FROM sale s
WHERE s.status = :status
GROUP BY s.employee_id
ORDER BY employee_id
LIMIT 100
```

严格来说，作者提交的是受限的 `object_sql_v1`，除了 SQL 文本，还要声明 `source_objects`、参数类型和结果列类型。SQL 引用的是已经发布的 Object/Field key，不是任意物理表，也不能取得裸数据库连接。作者负责“查什么、怎么算”；Report 和 Runtime 自动负责“允许查什么、如何安全执行”。

| 作者不需要重复实现的系统问题 | 框架自动保障 |
| --- | --- |
| SQL 能否访问任意内部表或未知字段 | SQL 先经过受限语法解析和语义绑定，只接受声明的数据源以及已发布、未禁用的 Object/Field；不支持的结构在定义发布前失败 |
| 报表会不会绕过当前用户的数据权限 | Runtime 为每个数据源编译当前 Permission 的 data scope；`all` 不追加范围条件，其余 scope 在 join、filter、group 和 aggregate 之前下推 |
| 多租户数据会不会在 JOIN 中串库 | 普通查询自动注入 Workspace predicate；特殊跨 Workspace 聚合要求明确执行模式、superadmin audience 和权限，并补充安全 join 条件 |
| 敏感字段会不会通过聚合泄露 | 执行前逐字段检查 read policy；不可读、已禁用、masked 或需要逐记录上下文判断的字段直接拒绝进入 Object SQL |
| 参数会不会造成 SQL 注入 | 作者声明 typed parameter，执行器使用方言参数绑定；参数内容不会拼接成 SQL 结构 |
| 报表增长后会不会无序、重复或静默截断 | 非分组多行定义要求明确的 `ORDER BY`，所有多行定义要求字面量 `LIMIT`；分组计划按分组项形成稳定顺序，执行使用 keyset cursor 并校验 cursor 形状和类型 |
| 查询会不会无限占用数据库 | 查询在只读事务中执行并应用 timeout；结果受显式业务上限和分页控制 |
| 金额在不同数据库上会不会精度漂移 | Object 字段的 precision/scale 进入编译计划，执行器按 SQLite/PostgreSQL/MySQL 方言处理精确 decimal/currency 并拒绝不安全 float |
| 报表导出期间权限或数据版本发生变化怎么办 | 异步导出重新解析 Principal，并校验授权范围、Report 定义、导出控制和 source version hash；变化时拒绝继续生成旧权限下的文件 |

所以这里的“灵活”不是允许作者绕过系统直接写任意 SQL，而是允许作者使用 SQL 的表达力，同时把租户隔离、RLS、字段权限、参数安全、方言、精度、分页和异步导出一致性收回到框架统一保证。

### 2.3 同一模式下的其他系统能力

下面的“使用者”主要指项目开发者、业务定义作者或模块接入者。他们表达项目差异，框架自动补齐跨项目重复、又最容易遗漏的系统保障。

#### 业务建模与执行

| 使用者只需要表达 | Runtime 自动保证 | 最终用户得到 |
| --- | --- | --- |
| 声明 Object 字段、关系、校验和能力 | Schema 物化、通用 CRUD、输入校验、Workspace 隔离、权限投影和一致的错误合同 | 新业务对象天然具备列表、详情、编辑、过滤等统一行为 |
| 调用 Record get/list/filter/sort/select | 查询规范化、分页边界、行级 scope、字段 read/mask、关联对象可见性和拒绝审计 | 同一用户在列表、详情和关联选择中看到一致的数据范围 |
| 声明 `append_only`、`soft_delete_only` 或 `immutable_after_state` | 所有 Mutation 入口统一执行生命周期规则；账本对象可增加 SHA-256 chain/HMAC 完整性校验和 replay | 已入账或已完成的数据不会被其他入口悄悄改写 |
| 为 Action 编写强类型 Handler 和业务判断 | 输入输出合同、Handler revision、精确 Object/Connector/File/Notification grant、Registry freeze、事务阶段和输出字段安全 | “批准、取消、结算”等业务动作有稳定边界，不能借业务代码越权 |
| 提交 create/update/delete/restore/conditional update 意图 | 统一授权、验证、事务、乐观并发、条件谓词、原子算术、Audit、Event/Outbox 和幂等 Receipt | 重复点击、并发修改和后台执行得到相同的业务结果 |
| 为高风险 Action 声明 assurance policy | Runtime 验证并消费与用户、Workspace、Action、Record 和 payload 绑定的 recent re-auth/OTP/maker-checker/workflow approval 证据 | 普通登录态不能直接完成敏感导出、审批或资金类动作 |
| 声明 Workflow 节点、边、审批人规则和超时 | 定义校验与快照、Process/Task/Event、any/all/sequential/quorum 审批、提醒升级、Timer、重试、幂等、Lease/Fencing 和人工恢复 | 用户能查看稳定的流程进度，即使定义升级或进程重启也不会丢失原流程语义 |
| 声明 Automation trigger、condition 和 instruction | before/after 阶段、变更字段/状态匹配、durable lifecycle event、correlation/causation、最大深度、幂等和失败处理 | 简单自动规则可靠执行，不会因为递归触发形成无限循环 |

#### 报表、异步任务与外部能力

| 使用者只需要表达 | Runtime/模块自动保证 | 最终用户得到 |
| --- | --- | --- |
| 声明 Report 数据集、维度、指标或 Object SQL | 定义和引用校验、权限 scope、查询规划、稳定分页、快照版本与受控导出 | 同一张报表会按当前用户权限自动收窄，不需要每张报表重写权限 SQL |
| 发起大批量导入或导出并提供授权后的 provider | Data Exchange 管理 Job、Chunk、Artifact、进度、取消、Lease/Fencing、有限重试、DLQ 和下载授权 | 大文件不堵塞页面请求，用户能看到进度、错误并恢复或下载结果 |
| 在 Action 中 stage 一个获权 Notification intent | Runtime 将通知与业务 Mutation 一起暂存，校验 event type、来源 identity、收件人和批次；Notification 处理模板、Inbox、偏好和渠道计划 | 业务成功后的提醒不会因进程瞬时故障永久丢失，也不会被无意重复创建 |
| 声明 Connector、Connection、operation，并实现项目特有 adapter | Runtime 注入受限 Transport，校验合同 hash、Action grant 和 read/reserve/write effect；外部写入强制 durable intent | 可以更换 ERP、短信或支付 Provider，而不需要重写业务 Action 和 Workflow |
| 对外部 `reserve/write` 调用 stage durable intent | Runtime 事务内写 `_publication_outbox`；Worker、Integration 和 Provider 使用稳定 identity、Receipt、重试、DLQ 与 reconciliation | 外部系统超时或短暂不可用时，业务状态仍可解释、可恢复 |
| 声明 Scheduler recurrence 和下游目标 | Scheduler 管理时区、preview、Run、misfire/catch-up、Lease/Fencing、重试、取消和 DLQ；下游仍走原 owner 用例 | 周期任务有下次运行时间、执行历史和恢复入口，不是不可见的 Cron 脚本 |
| 声明 Agent/Skill/Task、允许的 Object/Action 和结果 | Agent 管理 Dialog/Task/Proposal/Provider Run/Tool ledger；Runtime 在每次真实 effect 前重新检查当前权限和风险策略 | AI 可以灵活推理和建议，但不能获得一次授权后永久越权执行 |
| 上传一个声明为文件的业务字段，或在 Action 中请求 clean verification | Runtime 校验对象/字段/权限、Workspace 路径、大小和内容类型，登记内容 hash；业务使用前验证扫描 Receipt 与文件 identity | 用户可以上传和下载业务附件，受限文件不会仅凭文件名被信任 |

#### 运行、扩展与交付

| 接入者只需要选择或提供 | Runtime 自动保证 | 项目获得的灵活性 |
| --- | --- | --- |
| 为某个能力注入 Module Factory 或 Remote Factory | Binding/Descriptor、协议、mode、capability 和必要 adapter 校验；Runtime 不复制模块私有状态 | 同一业务可以先共进程部署，再按容量和隔离需求演进为 SaaS |
| 模块提供自己的 typed HTTP Adapter、公开 SDK/客户端和 usage 文档 | Runtime 统一挂载认证、listener exposure 与容量治理，但不聚合或托管 API 文档 | 模块可独立演进产品 API，而项目仍保持统一入口和治理 |
| 提供新的 Connector ProviderSet | Runtime 注入 HTTP/MQTT/filesystem/process 等受控 Transport；process 默认拒绝并使用 executable/working-directory allowlist | 新增供应商适配不要求修改 Runtime，也不能绕过宿主传输策略 |
| 在项目中提供 Business Handler | Runtime 只通过稳定 `runtimeext` 注册和调用，并在启动时冻结、计算 Registry hash | 项目可以自由实现领域逻辑，同时不接触 Runtime 内部 Service/Repository |
| 选择 SQLite、PostgreSQL 或 MySQL | Runtime 和嵌入模块复用宿主方言、事务、迁移锁和唯一 `_schema_migrations`；持久化通过 domainry-orm 渲染 | 开发、单机交付和服务化环境可以选择不同数据库而不复制业务代码 |
| 提供 source-owned frontend bundle | 前端宿主按项目定义的入口挂载制品；Runtime 只校验制品 hash、archive 安全并提供 `/api`，不识别业务端、管理端或门户端产品壳 | 前端页面、品牌和交互可以完全项目化，后端治理保持不变 |
| 提供已签名的项目 Runtime 制品 | 启动时校验 binary、Project、Generated SDK、Handler/Connector Registry 和前端 hash；运行中校验 schema/registry 与 release cohort | 错版本、半升级或同库混跑的不兼容实例会在接流量前失败，而不是污染业务数据后才暴露 |
| 提供项目配置与 i18n 扩展 | Runtime 从明确的项目扩展入口加载配置和本地化资源，不要求修改 Runtime 源码 | 同一能力可按项目调整配置、语言和展示文案 |

### 2.4 这些系统能力如何体现框架灵活性

Domainry 的灵活性来自“变化点和保障点分离”：

- Object 可以先使用通用 Record；业务复杂后，把某次更新提升为 Action，不需要替换数据权限和 Mutation 底座；
- 同一个 Action 可以由页面、Workflow、Automation、Scheduler 或 Agent 触发，业务不变量只实现一次；
- 同一份 Report SQL 会随 Principal 和 Workspace 自动得到不同的安全结果，作者不需要为每个角色复制报表；
- Connector Provider 可以替换，Action 仍引用稳定 Connector operation；
- 模块可以从进程内切换到远程服务，业务调用方仍依赖相同 SDK Binding；
- 前端、语言、数据库和部署单元可以变化，权限、审计、一致性和恢复规则仍由相同合同约束。

这不是“什么都可以动态修改”。Handler/Connector Registry 在启动后冻结，模块拓扑由项目组合决定，source-owned 定义必须经过校验和发布。框架允许在明确扩展点内变化，同时让变化不能绕过系统级保障。

## 3. Runtime、项目代码和外部模块如何分工

| Owner | 负责 | 不负责 |
| --- | --- | --- |
| 项目业务源码 | 业务对象、Action Handler、项目 Connector adapter、业务规则 | Runtime 内部事务、通用权限引擎、模块私有状态机 |
| Runtime | Record、Action、Workflow、Automation、统一写入、宿主授权、模块组装、运行治理 | Identity 账号状态、通知投递状态、Integration 凭证、Agent 执行状态等模块事实 |
| 外部能力模块 | 自己的领域规则、数据、migration、worker、产品 HTTP Adapter | 读取 Runtime 私有仓储或复制 Runtime 业务真相 |
| Connector Provider | 某个外部供应商协议的具体实现 | Connection、Credential、Invocation 生命周期和项目业务规则 |

Module 形态下，外部模块可以借用宿主数据库、事务边界、SQL 方言、迁移锁和唯一 `_schema_migrations` 台账，但数据的业务所有权仍属于模块。SaaS 形态下，模块拥有独立状态，Runtime 与模块通过幂等、Receipt、重试和对账协作，不伪装成数据库级分布式事务。

## 4. Runtime 核心能力：普通做法、Domainry 做法与同等保障成本

### 4.1 普通 Object 与通用 Record 能力

**普通开发常见做法**

为每个实体分别创建表、ORM Model、Repository、CRUD Service、Controller、分页、过滤、字段校验、软删除和导出接口。相同规则经常在不同入口重复实现。

**Domainry 做法**

项目通过 `ObjectSchema` 声明字段、校验、可用能力、生命周期和账本策略。Runtime 据此提供统一的查询、过滤、排序、分页、关联、创建、修改、删除、恢复和导出能力，并在入口处统一应用授权与字段策略。

**正例**

CRM 的客户备注、标签、联系人地址属于常规可变数据，适合声明为 Object 并使用通用 Record 能力。

**常规开发达到同等保障的代价**

除了为每个实体编写 CRUD，还要统一建设字段和关系校验、查询 DSL、分页、数据权限、字段脱敏、软删除、导出控制和 Schema 演进。对于“批准采购单”一类受控字段，又必须关闭通用更新入口、补充专用命令接口，并证明 HTTP、批处理和后台任务都不能绕过这条限制。缺少统一执行层时，这些规则会重复分布在 Controller、Service、Repository 和导出代码中。

**解决的问题**

- 消除重复 CRUD 和查询基础设施；
- 避免 HTTP、批处理和后台任务各自实现一套校验；
- 通过 `append_only`、`soft_delete_only`、`immutable_after_state` 等生命周期约束，防止通用接口越过业务不变量；
- 让字段、能力、权限和 API 投影来自同一份定义。

### 4.2 具名 Action 与最小能力 Handler

**普通开发常见做法**

Controller 调用一个持有数据库、HTTP Client、消息队列和所有 Repository 的 Service。只要取得 Service，就可能读写整个系统，实际权限依赖开发者自觉。

**Domainry 做法**

每个 Action 声明输入输出合同、风险、前置条件、审计事件、并发策略、保障方式和 Effect Set。生成的 Handler 只获得该 Action 被授予的 Object、Connector、文件和通知能力；Runtime 在启动时校验合同哈希、Handler revision 和授权清单并冻结 Registry。

**正例**

`order.cancel` 只获得：

- 读取并条件修改当前订单；
- 创建一条取消记录；
- 投递指定退款 Connector operation；
- 发布 `order.cancelled` 通知事件。

它不能因为“同在订单服务里”就读取工资数据或调用任意外部连接。

**常规开发达到同等保障的代价**

每个业务命令都要自行维护请求/响应 DTO、合同版本、授权检查、前置条件、乐观并发、幂等、审计事件和副作用白名单；还要设计一种机制，确保 Service 只能访问获权 Repository 和外部操作。否则常见实现只能把整个 service container 交给业务代码，再依靠代码评审阻止越权。合同升级时，还需要额外门禁发现接口、SDK 和 Handler 已经不匹配。

**解决的问题**

- 把“代码理论上能做什么”收紧成“本 Action 被允许做什么”；
- 在编译、启动和执行阶段发现合同漂移；
- 让高风险操作显式要求 recent re-auth、OTP、maker-checker 或 workflow approval；
- 防止项目业务代码取得 Runtime service container。

### 4.3 统一 Mutation 与并发控制

**普通开发常见做法**

用户接口走 Service 校验，定时任务直接更新 Repository，工作流执行器再写一套状态转换。并发时常以“先查库存，再减库存”实现，产生超卖或丢失更新。

**Domainry 做法**

HTTP、Action、Workflow、Automation、Scheduler 和 Agent 触发的业务写入最终都进入同一 Mutation Kernel。执行顺序统一为授权、构建不可变上下文、规划 Mutation、校验、事务提交，并携带预期版本、条件谓词或原子算术更新。

**正例**

课程预约要求剩余名额大于零。Action 使用 conditional update 表达“`remaining > 0` 时原子减一”，并携带稳定业务错误，而不是在应用层先查询后更新。

**常规开发达到同等保障的代价**

团队需要先建设一套所有写入口都必须调用的 Command/Mutation 层，再逐一改造 HTTP、Workflow、定时任务、导入和 AI 工具，禁止它们直接访问 Repository。同时还要为不同数据库实现并验证条件更新、版本冲突、原子算术、审计、事件和幂等提交。只要保留一个“内部快速写库”入口，就仍然存在绕过不变量的风险。

**解决的问题**

- 防止不同入口绕过验证、权限、生命周期或审计；
- 防止 lost update、超卖和重复执行；
- 统一错误语义、幂等结果和执行证据；
- 让新入口复用现有不变量，而不是复制业务逻辑。

### 4.4 数据库事务与外部副作用

**普通开发常见做法**

在数据库事务中调用支付、邮件或 Webhook；或者先提交数据库，再用 goroutine 异步发送。前者会持有长事务且无法真正回滚外部系统，后者在进程崩溃时会丢失任务。

**Domainry 做法**

- 外部只读调用只能在 Action 写入前同步执行；
- 外部 `reserve` 或 `write` 必须使用 durable intent；
- 业务 Mutation、Audit、Workflow/Event Intent 和 `_publication_outbox` 在本地事务内提交；
- Worker 使用稳定 message/dedup identity 把任务交给 Integration；
- Integration 持有 Provider invocation、Receipt 和不确定结果对账。

**正例**

订单确认时，订单状态和“通知 ERP 发货”的 Outbox 事实一起提交。即使进程在提交后立刻崩溃，Worker 仍能恢复并幂等交接。

**常规开发达到同等保障的代价**

不能只把外部调用放进 goroutine 或 after-commit hook。要达到同等保障，需要建设事务内 Outbox、稳定 message/dedup identity、带 Lease/Fencing 的 Worker、有限重试、DLQ、Provider 幂等、远端 Receipt、不确定结果对账、补偿策略和受权限控制的人工恢复入口，还要用故障注入验证“提交前崩溃、提交后崩溃、网络超时、重复回包”等路径。

**解决的问题**

- 防止业务已成功但通知永久丢失；
- 防止网络超时导致重复调用 Provider；
- 明确 Runtime、Integration 和 Provider 各自负责的恢复范围；
- 避免用伪分布式事务掩盖失败状态。

### 4.5 Workflow、Automation、Scheduler 和 Record Timer 的选择

这四种能力都会“晚一点做事”，但所有权不同。

| 能力 | 用户场景正例 | 常规开发达到同等保障的代价 | 选择理由 |
| --- | --- | --- | --- |
| Workflow | 采购单经过主管、财务审批，超时升级，失败可人工重试 | 自建流程定义/实例、节点、任务、事件、审批人解析、撤回退回、Timer、提醒升级、重试、版本快照和操作台 | 有多节点、等待、人工决策和过程状态 |
| Automation | 客户升级为 VIP 后自动打标签并发布通知 | 自建字段/状态变化检测、规则匹配、durable event、幂等执行、失败重试和循环/递归保护 | 对一次记录生命周期事件做无状态规则响应 |
| Scheduler | 每月 1 日运行结算流程；每天刷新报表快照 | 自建 Schedule/Run 状态、Cron 解析、时区、misfire/catch-up、Lease/Fencing、重试、DLQ、取消和运行历史 | 由日历或周期驱动 recurrence/run |
| Record Timer | 某条合同的 `expires_at` 到期后触发动作 | 自建记录级 Timer、字段变更后的重排/取消、持久化扫描、并发 claim、崩溃恢复和重复触发保护 | 时间属于具体业务记录，不属于全局 recurrence |

**普通开发常见问题**

所有延时逻辑都塞进 Cron，或者所有自动响应都创建一条 Workflow。结果是 Cron 里包含大量业务判断，Workflow 又被简单字段同步淹没。

**Domainry 解决方式**

按“过程状态、记录事件、周期计划、记录时间点”区分 owner，并分别提供幂等、重试、Lease、Fencing 和人工处置语义。

### 4.6 身份、数据权限与高风险操作

**普通开发常见做法**

接口中判断 `role == admin`，Repository 默认查询全部数据；导出、Agent tool 或后台任务再各自补权限判断。业务联系人和登录用户也容易混成同一个 User 表。

**Domainry 做法**

Identity 管理登录主体、组织、角色、权限和 session；Runtime 解析当前 Principal、Workspace、行级范围、字段可见性、引用可见性和导出策略。Action 可以额外要求风险保障方法，并在 Agent 每次执行具体 tool effect 时重新授权。

**正例**

HR 主管只能查看自己组织范围内的员工，薪资字段仅对薪酬角色可见，批量导出还要求 recent re-auth。

**常规开发达到同等保障的代价**

需要独立建设账号、凭证、Session/Token、组织角色、服务身份和授权目录，并让列表、详情、关联、导出、Workflow、后台任务和 Agent 工具执行相同的行级与字段级策略。高风险操作还要维护 re-auth/OTP/双人复核凭据及失效时间。若把登录用户与 CRM 联系人等业务人物混在一张表，后续还要承担身份生命周期和业务生命周期相互污染的迁移成本。

**解决的问题**

- 区分认证主体与普通业务人物；
- 避免列表、详情、关联、导出和后台入口权限不一致；
- 防止高风险能力仅靠普通登录态执行；
- 让模块只消费必要授权事实，不复制 Identity 模型。

### 4.7 Report、Data Exchange 与普通查询

**普通开发常见做法**

列表、统计、CSV 导出共用一个 Controller。数据量增长后，同步生成大文件造成超时和内存膨胀，报表 SQL 又绕过行级与字段权限。

**Domainry 做法**

- 普通 Record list 负责在线记录查询；
- Report 负责聚合、分组、计算、稳定分页、快照和导出准备；
- Data Exchange 负责大文件 job、chunk、artifact、进度、取消、重试与下载；
- Runtime 提供已经授权的数据读取端口，而不是让模块任意访问 Runtime 表。

**正例**

“按区域和产品统计季度收入”属于 Report；将 50 万条明细导出为 CSV 的任务生命周期属于 Data Exchange。

**常规开发达到同等保障的代价**

不仅要编写聚合 SQL，还要建设报表定义版本、查询规划、字段和行权限、稳定分页、快照 claim/fencing、结果可复现和导出审计。大文件侧还需要 Job/Chunk/Artifact、流式编解码、完整校验后应用、进度、取消、TTL、下载授权、断点恢复和失败重试；否则同步导出很容易演变成超时、内存膨胀和权限旁路。

**解决的问题**

- 区分交互式查询、分析查询和大文件任务；
- 避免长请求、内存失控和不可恢复的导出；
- 防止报表/导出绕过当前用户的数据与字段权限；
- 明确 Report 定义与 Data Exchange artifact 的不同所有权。

### 4.8 可恢复运行与运维治理

**普通开发常见做法**

异步失败只写日志；管理员通过改数据库、重启服务或手工重发消息恢复。重复点击“重试”可能再次产生业务效果。

**Domainry 做法**

Runtime 和模块为长任务提供幂等 Receipt、Run/Event、Lease、Heartbeat、Fencing、有限重试和 DLQ，并提供查询、预览、重试、解除租约、诊断、Break-glass 等受治理操作。

**正例**

某次 ERP 交接连续失败进入 DLQ。运维人员查看失败证据，在修复连接后针对稳定 message identity 重试，不重新执行订单业务 Mutation。

**常规开发达到同等保障的代价**

每类异步任务都要建立持久执行状态、幂等 Receipt、Lease/Heartbeat/Fencing、重试上限、DLQ 和诊断字段，并提供查询、预览、重试、取消、强制释放、Break-glass 等管理员接口及权限审计。仅写日志还不足以回答任务是否提交、是否被旧 Worker 覆盖、重试会不会重复业务效果，因此还需要相应的并发和故障恢复测试。

**解决的问题**

- 让失败成为可查询、可恢复的状态，而不是一行日志；
- 防止旧 Worker 或重复请求覆盖新执行结果；
- 为人工恢复提供权限、证据和可追踪入口；
- 区分 Monitoring、Audit、业务执行状态与技术日志。

### 4.9 Module 与 SaaS 拓扑

**普通开发常见做法**

项目一开始在“所有能力都塞进单体”与“全部拆成微服务”之间二选一。后续拆分时，业务代码已经直接依赖实现类、表和内部 HTTP 路径。

**Domainry 做法**

项目组合通过 SDK Factory/Binding 选择 Module 或 SaaS。产品调用方依赖稳定能力合同；模块拥有自己的状态、migration、worker 和 HTTP Adapter。拓扑切换不能改变 owner，也不能把本地事务语义假装延伸到远端。

**正例**

早期项目将 Scheduler 作为 Module 运行，共享宿主数据库并减少部署单元；任务规模增长后，在完成 worker fencing、状态迁移和切流校验后切换到 Scheduler SaaS。

**常规开发达到同等保障的代价**

每项可拆分能力都要先定义稳定 SDK/HTTP 合同、模式与版本协商、认证 audience、超时、幂等、Receipt 和不可用策略，再分别实现本地与远程 Binding、产品 HTTP 语义一致性、状态与 Worker 所有权、迁移切流、双 Worker fencing 和回滚验证。单纯把函数改成 HTTP 调用只完成了传输替换，没有完成一致性和运维语义。

**解决的问题**

- 避免为了未来规模过早承担微服务复杂度；
- 允许专业能力独立扩缩容和故障隔离；
- 保持 Module/SaaS 产品语义和前端路径稳定；
- 强制暴露真正的跨进程一致性成本。

## 5. 外部能力模块的用户价值与同等保障成本

| 模块 | 用户能感知到的能力 | 普通项目通常怎么做 | Domainry 怎么做 | 用户场景正例 | 常规开发达到同等保障的代价 | 主要解决的问题 |
| --- | --- | --- | --- | --- | --- | --- |
| Identity | 登录、单点登录、账号与组织管理；不同岗位看到不同数据和操作 | 自建 User/Role 表，在中间件中零散判断角色 | 独立拥有认证、Session、Principal、组织、角色、权限与授权目录 | 员工登录、服务身份、OIDC/SAML、组织权限 | 建设密码/外部登录、Token/Session 轮换、组织角色、权限目录、应用身份、审计和所有入口一致的数据策略 | 认证与业务人物混淆、权限规则漂移 |
| Notification | 消息中心、待处理提醒、已读归档、代理和通知偏好 | 事务后直接发邮件；各业务模块维护模板和已读状态 | 独立拥有模板、Inbox、偏好、Channel plan、投递状态和 Worker | 审批提醒、站内信、告警、摘要 | 建设模板版本、收件人解析、Inbox、偏好、代理、聚合摘要、渠道计划、幂等 Worker、失败重试和投递查询 | 通知丢失、重复发送、模板和偏好分散 |
| Monitoring | 运维人员查看系统健康、积压、存储和迁移状态 | 每个模块各写健康接口和指标格式 | 聚合 owner observation、readiness 和迁移 telemetry | 运行健康、发布检查、故障定位 | 统一各 owner observation、健康判级、指标窗口、存储/迁移 readiness、保留策略、查询 API 和跨拓扑合同测试 | 监控口径不一；与审计、日志混淆 |
| Scheduler | 查看周期任务、执行历史、下次运行时间、失败与重试 | Cron 直接调用业务 Repository | 独立拥有 schedule、run、misfire、retry、lease 和 DLQ，下游只调用 owner 用例 | 周期结算、定时同步、快照刷新 | 建设 Schedule/Run/DLQ、Cron 与时区、misfire/catch-up、Lease/Fencing、幂等下游回执、取消恢复和运维页面 | 定时逻辑不可追踪、重复执行、双 Worker |
| Agent | 自然语言问答、分析建议、任务进度、提案确认和受控执行 | LLM 直接拿数据库和全部工具；对话状态只在内存 | 独立拥有 Dialog/Task/Proposal/Provider Run/Tool ledger，Runtime 每次重授具体 effect | 合同风险分析、自然语言业务操作、受控半自动执行 | 建设会话、Task/Proposal/Approval、Provider Run、Tool ledger、用量、结构化输出、Lease/Retry/Reconcile，并在每次工具调用重新授权和留证 | AI 越权、重复工具调用、过程不可审计 |
| Data Exchange | 上传大文件、查看校验和处理进度、取消任务、下载结果 | HTTP 请求内一次性读写 CSV | 独立拥有 Job、Chunk、Artifact、进度、取消、Lease 和恢复 | 50 万行导入导出、大文件后台任务 | 建设流式 CSV、Job/Chunk/Artifact、完整校验后应用、进度、取消、TTL、下载授权、游标断点、Lease、重试和 DLQ | 超时、内存膨胀、部分导入和任务丢失 |
| Integration | 管理外部系统连接，接收 Webhook，并查看同步或调用状态 | 业务 Service 保存 API Key 并直接调用供应商 | 独立拥有 Connection、Credential、Webhook、Provider Task、Invocation 和对账 | ERP、支付、短信、Webhook、Web Push | 建设连接和密钥生命周期、Webhook inbox/验签、映射、Provider task、幂等 invocation、Receipt、不确定结果对账、凭证轮换和管理面 | 密钥泄露、供应商状态渗入业务、网络失败难恢复 |
| Metadata | 看到一致的多语言标签、字典选项和 Workspace 个性化配置 | 把字典、标签、定义版本复制到各业务表 | 独立拥有定义版本、本地化、字典、投影和 Workspace preference | 多语言字段标签、状态字典、定义发现 | 建设版本化定义仓、发布/恢复、语言覆盖、字典查询、Workspace preference/rule set、缓存失效和多实例 revision 一致性 | 元数据多份真相、定义版本和展示漂移 |
| Lifecycle | 管理保留策略、法律冻结、主体数据访问/删除请求并查看执行证据 | 定时脚本直接 DELETE；隐私请求靠工单人工执行 | 独立拥有 retention、legal hold、subject request、cleanup 和删除证据 | GDPR/隐私删除、保留期、法律冻结 | 建设策略、Legal Hold、Subject Request、跨 owner export/erase、预览、分批清理、归档证据、外部删除对账、备份删除重放和审计 | 合规删除不可证明、误删、跨 owner 无恢复证据 |
| Report | 筛选、分组和查看经营报表，刷新快照，发起受控导出 | Controller 中手写聚合 SQL 并直接导出 | 独立拥有报表定义、查询规划、聚合、快照和导出策略 | 经营分析、分组统计、受权限约束的快照 | 建设版本化报表定义、查询规划和方言、聚合计算、稳定分页、字段/数据权限、快照 Lease/Fencing、导出策略和结果复现 | 报表规则散落、权限绕过、结果不可复现 |
| Audit | 查询某条业务数据由谁、在何时、经过什么动作变更，并按权限导出 | 打一行日志或在业务表存 `updated_by` | 独立追加不可变事件，并在 Module 形态支持与业务 Mutation 同事务提交 | 合规追踪、Actor 历史、审批证据 | 建设不可变事件模型、与业务写入原子提交、Actor/Subject 索引、字段投影权限、留存、导出、去重和生命周期证据 | 日志被覆盖或缺字段，无法形成可靠业务证据 |
| Connectors | 在产品中选择并连接具体 ERP、支付、短信等供应商 | 各项目随意封装第三方 SDK，自己创建网络 Client | Provider 只实现供应商协议，通过 Runtime 注入的受限 Transport 执行 | 某 ERP、消息或支付厂商适配 | 为每个供应商维护协议、Schema、错误规范化、受限 Transport、超时、测试替身和版本兼容，并阻止 Provider 旁路宿主网络治理 | 第三方依赖扩散、网络旁路、Provider 与业务耦合 |

### 5.1 容易混淆的模块边界

- **Identity 与业务 Profile/联系人**：能登录和被授权的是 Identity；项目中的会员、患者、客户、供应商联系人可以只是业务 Record。
- **Notification 与 Integration**：Notification 决定通知模板、收件人、偏好和生命周期；Integration 负责具体外部 Channel/Provider 的连接、凭证和调用证据。
- **Monitoring、Audit 与日志**：Monitoring 回答“系统是否健康”；Audit 回答“谁做了什么”；日志和 Trace 回答“代码执行时发生了什么”。
- **Scheduler 与 Workflow Timer**：Scheduler 拥有 recurrence；Workflow Timer 属于某个流程实例的等待节点。
- **Report 与 Data Exchange**：Report 决定查什么、怎么算、允许导出什么；Data Exchange 负责如何可靠地产生和交付大文件。
- **Agent 与 Workflow**：Agent 处理需要语言理解和推理的不确定步骤；Workflow 持有确定的业务过程、等待和审批状态。Agent 不能成为绕过 Workflow 的第二套流程引擎。

## 6. 一个端到端对比例子：采购单审批并同步 ERP

### 6.1 普通项目的常见实现

1. `purchase_orders` 表包含一个可任意更新的 `status` 字段；
2. Controller 检查当前角色后把状态改成 `approved`；
3. 同一个 Service 中调用 ERP HTTP API；
4. 调用成功后发邮件；
5. Cron 定期扫描长时间未审批的数据；
6. 失败只写日志，管理员修改数据库后重新执行；
7. 报表直接查询采购表，另写一份部门过滤条件。

这种实现首次交付可能很快，但会逐渐出现：其他接口可绕过审批、重复请求导致 ERP 重复创建、数据库成功而消息丢失、部门权限不一致、Cron 与人工操作并发、审计证据不完整。

### 6.2 Domainry 的实现分工

1. **Object**：采购单字段、关系、校验和生命周期由业务定义声明；批准后需要保护的字段使用生命周期策略约束；
2. **Action**：`purchase_order.submit`、`approve`、`reject` 是具名动作，声明输入输出、前置条件、并发版本、风险和审计事件；
3. **Identity + Runtime 授权**：提交人、部门主管、财务审批人获得不同 Action 和数据范围；
4. **Workflow**：持有主管审批、财务审批、退回、超时升级和人工重试状态；
5. **Mutation Kernel**：批准时统一验证当前状态、版本、权限和业务字段，并提交采购单变化、Workflow 事件和 Audit；
6. **Integration + Outbox**：同一事务写入 ERP durable handoff，Integration 幂等调用 Provider 并保存 invocation/receipt；
7. **Notification**：根据业务事件、收件人和偏好创建 Inbox 或外部渠道投递；
8. **Scheduler/Timer**：周期性任务使用 Scheduler；某个审批节点的截止时间属于 Workflow Timer；
9. **Report**：按当前用户数据范围统计部门采购金额；大文件通过 Data Exchange 交付；
10. **Monitoring/Operations**：观察积压、失败与 DLQ，并针对原执行 identity 恢复，不重新批准采购单。

### 6.3 这个设计实际换来了什么

- Action 成为唯一业务命令边界，客户端不能直接伪造批准状态；
- 本地事务失败时不会留下 ERP handoff；事务成功后 handoff 不会因进程崩溃永久丢失；
- 重试的是同一个业务 identity，不是重新执行一遍业务动作；
- HTTP、Workflow、后台任务和 Agent 使用同一授权与 Mutation 规则；
- 报表、通知、审计和运维各自读取所属 owner 的真相，不复制主业务状态机。

代价是必须认真建模 Action、Workflow、owner 和一致性边界。对于只有两张表、没有审批、外部副作用和治理要求的小工具，这些投入可能没有收益。

## 7. 对普通开发者的直接优势

| 开发活动 | 普通项目需要自行建设 | 使用 Runtime 后主要关注 | 最终用户感知到的变化 |
| --- | --- | --- | --- |
| 新增普通业务实体 | 表、ORM、CRUD、分页、验证、权限、审计 | Object 字段、关系、生命周期和业务权限 | 列表、详情、搜索、校验和权限行为更一致 |
| 新增业务命令 | Controller、Service、事务、权限、幂等、副作用 | Action 合同、Handler 领域逻辑和精确 capability | 获得有业务含义的操作、稳定错误和防重复结果 |
| 新增审批过程 | 状态表、任务表、超时、提醒、重试、操作台 | Workflow 图、审批 resolver、Action 节点和异常策略 | 有待办、过程进度、退回/撤回、提醒和异常处理入口 |
| 接入外部系统 | 凭证、HTTP Client、重试、幂等、Webhook、对账 | Connection/Connector 声明和项目特有 adapter | 能看连接与同步状态，失败不会静默消失 |
| 增加批量导入导出 | 文件解析、任务表、进度、分块、失败恢复 | 授权后的 provider 与格式/映射规则 | 大文件操作有进度、取消、错误结果和可恢复下载 |
| 引入 AI | 会话、工具权限、审批、任务状态、费用与重试 | Agent/Skill 定义、允许的工具与人工决策点 | AI 建议与真实业务执行有清晰边界，重要动作可确认 |
| 上线运维 | 探针、队列状态、DLQ、诊断和人工恢复脚本 | 容量参数、告警阈值、运行手册和治理权限 | 服务更稳定，失败任务可查询、可解释、可恢复 |

最大的收益不是“完全不写代码”，而是把开发投入从重复的横向基础设施转移到项目真正不同的领域规则，并让默认执行路径具备一致性、安全和可恢复性。

## 8. 适配场景

### 8.1 强适配

- CRM、ERP、订单、合同、采购、财务、人事、客服等中后台业务系统；
- 多角色、多组织、Workspace 隔离和复杂数据权限；
- 审批多、过程长、需要撤回、重试和人工处置；
- 有审计、数据保留、主体删除和法律冻结要求；
- 同时存在第三方集成、通知、调度和大批量数据交换；
- 希望引入 AI，但要求工具调用和业务写入仍受 Runtime 权限与治理约束；
- 初期需要低部署复杂度，后期可能将专业能力独立服务化。

### 8.2 可以使用，但应控制能力范围

- 中等规模 CRUD 系统：可以只使用 Object、Record、Identity 和必要模块，不应为了“完整”启用所有能力；
- 少量审批或定时任务：先判断 Automation、Workflow、Scheduler 哪一个 owner 最准确；
- 报表较少：在线列表和简单聚合足够时，不应提前引入复杂快照和大文件链路。

### 8.3 不适合或需要额外架构

- 官网、展示页、一次性工具和极小型 CRUD；
- 极低延迟、高频流式计算或实时撮合核心；
- 要求跨服务强 ACID，而无法接受最终一致性和补偿的系统；
- 需要完整 BPMN 语义、任意脚本编排，而当前 Workflow Graph 无法表达的系统；
- 离线、边缘端或资源极度受限且不能承担 Runtime/Module 治理成本的应用。
