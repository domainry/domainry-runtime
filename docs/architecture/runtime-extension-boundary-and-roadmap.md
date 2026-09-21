# Runtime 扩展边界与实施路线

状态：P0、P1 已完成并完成公开合同审计；P2 保持按需评审
适用范围：Domainry Runtime、Runtime 依赖模块、项目静态组合层
依据：当前仓库、当前依赖模块及其测试中的已实现合同；不以历史设计稿代替代码事实

## 1. 目的

本文明确 Runtime 还需要补充哪些扩展能力、扩展应开放到什么层级、哪些能力不应做成扩展，以及相应实施顺序和验收标准。

目标不是让 Runtime 可以执行任意项目代码，而是在不破坏权限、事务、幂等、审计、发布一致性和模块所有权的前提下，避免固定框架能力阻断正常业务开发。

## 2. 结论

Runtime 需要继续扩展，但只开放少数受治理的能力边界：

1. 流程找人需要受控的项目扩展接口。
2. Scheduler 需要一等的 Business Action 目标。
3. 业务日历、关系型数据权限、文件类型、对象存储和封闭结构化字段已经补齐。
4. 所有可执行项目扩展必须被冻结、可描述并进入发布身份。
5. CRUD Hook、任意 Workflow 节点、任意 Agent Tool、任意字段插件和运行时脚本不应开放。

复杂业务规则继续由命名 Business Operation 和 Handler 承担。普通 CRUD 只负责普通字段持久化，不能再增加一套隐藏的项目 Mutation Hook。

## 3. “用户自定义”的范围

本文中的自定义分为三个层级：

| 层级 | 是否允许 | 边界 |
| --- | --- | --- |
| 项目开发者 | 允许 | 在项目构建时提供编译后的扩展实现，声明 descriptor 和最小能力 |
| 租户管理员 | 允许 | 在已发布扩展中按稳定 key 选择，并填写通过 schema 校验的配置 |
| 普通业务用户 | 不允许执行代码 | 只能使用产品已发布的配置能力，不能上传 Go、脚本、SQL 或动态插件 |

所有项目扩展采用静态组合、启动注册和启动冻结。当前不支持热替换、运行时下载插件或按 Workspace 加载二进制代码。

## 4. 设计原则

### 4.1 扩展行为必须可进入发布身份

任何可能改变业务执行结果的项目扩展都必须具备稳定 descriptor。descriptor 至少包含扩展类型、稳定 key、revision、输入或配置合同 hash，以及声明的能力。

Runtime 必须在接收流量前完成以下动作：

1. 校验 descriptor。
2. 拒绝重复 key。
3. 校验扩展引用。
4. 冻结注册表。
5. 将完整扩展 descriptor 清单计入 Runtime release identity。

项目源码 hash 不能代替扩展注册表 hash，因为同一份项目代码可能根据组合输入返回不同的扩展实例或 descriptor。

### 4.2 扩展只能获得生成的窄能力

扩展不得获得数据库连接、Repository、Runtime 内部 Service、裸事务、任意网络访问或通用 Runtime Client。

需要读取业务数据时，由 Runtime 根据 descriptor 生成只读能力；需要写业务数据时，继续通过 Business Action 的事务能力执行。

### 4.3 模块扩展与业务扩展分离

- Business Handler、Assignee Resolver、Workspace Bootstrap Participant 属于项目业务扩展。
- Blob Store、File Scanner、模块 Factory 属于部署组合或基础设施适配。
- Workflow、Scheduler、Notification 等模块协议只暴露其 owner 所需的稳定能力，不暴露通用执行器。

### 4.4 声明式能力优先，代码扩展兜底

高频且语义稳定的需求优先建模为声明式能力，例如关系找人、经理链和业务日历。只有声明式能力不能真实表达的项目算法才进入受控代码扩展。

## 5. P0：现在实施

### 5.1 [x] 统一 Project Extension 注册和发布身份

#### 实施前根本原因

`runtimeext.ExtensionSet` 已同时包含 `BusinessHandlers` 和 `WorkspaceBootstrapParticipant`，但 `BusinessHandlerRegistry.Descriptors()` 只返回 Handler descriptor，`runtimeReleaseIdentity` 也只计算 Handler 和 Connector registry hash。

因此 Workspace Bootstrap Participant 的 revision、输入合同或记录能力发生变化时，不一定反映在项目扩展注册身份中。随着更多扩展加入，这个缺口会继续扩大。

#### 实施范围

- 将 `runtimehost.Options.BusinessHandlers` 直接替换为 `ProjectExtensions` 或 `ProjectExtensionFactory`。
- 建立统一的项目扩展注册边界，至少覆盖：
  - Business Handler；
  - Workspace Bootstrap Participant；
  - 后续 Workflow Assignee Resolver。
- 每种扩展分别负责自身 descriptor 规范、重复检测和引用校验。
- 增加统一的 `ProjectExtensionRegistrySHA256`，或等价的分类型 hash 加组合 hash。
- readiness drift 校验必须比较完整项目扩展身份。
- 不保留旧字段、旧注册入口或双轨兼容逻辑。

#### 不在范围内

- 通用 `Execute(any)` 插件接口。
- 运行时替换或注销扩展。
- 按 Workspace 加载不同二进制实现。

#### 验收标准

- 只修改 Workspace Bootstrap Participant revision，release combination hash 必须变化。
- 任一扩展 key 重复时启动失败。
- 注册表冻结后不能注册、替换或注销扩展。
- Manifest 引用了未注册扩展时，发布或启动失败，不能延迟到业务执行期。

#### 实施结果

`runtimehost.Options.ProjectExtensions` 现在是唯一项目扩展入口。Business Handler、Workspace Bootstrap Participant 和 Workflow Assignee Resolver 分别完成 descriptor 校验、重复 key 拒绝、注册冻结和引用校验；组合后的 `ProjectExtensionRegistrySHA256` 进入 Runtime release identity 与 readiness drift 判定。旧的独立 Handler 入口未保留。

### 5.2 [x] 修正 Workflow 找人结果模型

#### 实施前根本原因

当前 `resolveApprovalAssignees` 返回 `[]string` 和一个 `roleKey`。多个 resolver 合并时，后一个非空角色会覆盖前一个角色，随后所有审批任务写入同一个 `AssigneeRoleKey`。候选人与其来源角色不是一一对应关系。

另外，当前名为 `record_field` 的 resolver 实际读取 `process.Variables[field]`，并没有读取业务记录，名称和行为不一致。

#### 实施范围

- 用逐候选人模型替代 `[]userID + roleKey`：

  ```go
  type ResolvedAssignee struct {
      UserID      string
      RoleKey     string
      ResolverKey string
      Evidence    AssigneeEvidence
  }
  ```

- 明确定义同一用户被多个 resolver 命中时的来源和角色合并规则。
- 将现有变量读取能力命名为 `variable_user`。
- 如果保留 `record_user_field`，它必须通过 Workflow 已持有的有界 Record Reader 读取真实业务记录。
- Approval、CC、Reminder 和 Escalation 复用同一套候选人模型。
- 节点激活时固化 electorate 和 resolver evidence；除非流程定义显式要求重新解析，否则组织变化不能修改已形成的审批选民。

#### 验收标准

- 两个 role resolver 产生不同用户时，每个任务保存自己的角色来源。
- 同一用户被多个 resolver 命中时，结果稳定且证据不丢失。
- `variable_user` 和 `record_user_field` 的数据源有独立测试。
- Approval、CC 和 Escalation 对相同 resolver 输入得到一致候选人。

#### 实施结果

找人结果已经改为逐候选人的 `ResolvedAssignee`，每个用户保留自己的 Role、resolver key 和证据；去重不会覆盖来源。`variable_user` 与真实 Record Reader 驱动的 `record_user_field` 已分离，Approval、CC、Reminder、Escalation 共用同一解析和证据固化路径。

### 5.3 [x] 增加受控 Workflow Assignee Resolver

#### 实施前根本原因

现有内建 resolver 只能表达固定用户、角色、经理和流程变量等简单规则。实例审批路由允许发起 Action 直接给出审批路线，但它不能完整覆盖：

- 自动触发且没有发起 Action 计算路线的流程；
- 多个审批节点分别使用不同动态找人规则；
- 根据业务记录关系、区域、金额或项目成员计算审批人；
- 审批、抄送和升级之间复用同一找人规则。

#### 第一阶段：补充内建声明式 resolver

- `variable_user`
- `record_user_field`
- `relation_user`
- `relation_role`
- `manager_chain`

内建 resolver 能覆盖的需求不得要求项目编写代码。

#### 第二阶段：项目 Assignee Resolver

项目扩展 descriptor 至少声明：

- resolver key 和 revision；
- 配置 schema 及其 hash；
- 允许读取的对象、字段和关系路径；
- 允许使用的 Identity 投影；
- 最大查询量和最大候选人数。

执行接口接收不可变的流程快照、节点配置和 Runtime 生成的只读能力，返回 `[]ResolvedAssignee`。返回值必须包含稳定的来源证据。

项目 Resolver 不得：

- 读写数据库或 Repository；
- 写业务记录；
- 调用 Connector 或网络；
- 读取未声明对象和字段；
- 使用未注入的当前时间、随机数等不确定输入。

#### 验收标准

- 未注册 resolver key 在 Workflow 发布阶段失败。
- descriptor 变化进入 Project Extension release hash。
- Resolver 超过读取范围、候选人数或执行预算时失败关闭。
- 执行结果和必要证据持久化，可供审计与问题定位。

#### 实施结果

内建 resolver 已覆盖变量、记录用户字段、关系用户、关系角色和经理链；项目 resolver 通过 `ProjectExtensions` 静态注册。Runtime 按 descriptor 生成有界只读能力并执行对象/字段/关系、Identity 投影、读取量和候选人数约束。`workflow.assignee_resolver` 的 authoring contract 同时披露内建与项目 descriptor，未注册引用在发布阶段失败。

### 5.4 [x] Scheduler 增加一等 `business_action` target

#### 实施前根本原因

Scheduler authoring 当前只允许 `workflow`、`report_snapshot_refresh` 和 `http`。当前在建的 `RunBusinessJob` 已能通过 Record Timer 安排一次性 Action，但不能表达每天、每周或 Cron 周期任务。

另外，Scheduler authoring 已暴露 `run_as_role`，但 Runtime 映射到 Scheduler Definition 时没有携带它，Dispatcher 最终固定使用系统 principal。这属于公开合同和真实执行不一致。

#### 实施范围

- Scheduler SDK authoring 增加 `business_action` target 类型。
- Target 明确携带 Action key、Object key 和 payload，不接受任意 owner 名称。
- Runtime 通过现有 Action Application Service 执行，复用 Action 权限、幂等、事务、错误合同和审计。
- `run_as_role` 解析为受管 service/workload principal。
- 校验目标 Action、Object 和 service role 在发布时存在。
- Scheduler run ID 和窗口 identity 必须形成稳定 Action idempotency key。

如果本阶段不实现 `run_as_role`，应直接从 authoring schema 删除，不能继续保留不生效字段。

#### 不在范围内

- 任意 Scheduler Target Executor 注册。
- Scheduler 直接调用 Handler，绕过 Action Application Service。
- 使用 installation system principal 静默提升业务 Action 权限。

#### 验收标准

- 相同 Scheduler window 重试不会重复提交业务结果。
- service role 不具备 Action 权限时执行失败关闭。
- 暂停、恢复、misfire 和 retry 不改变 Action 授权身份。
- `RunBusinessJob` 继续只承担一次性业务后台任务，不与周期 Scheduler 形成第二套协议。

#### 实施结果

Scheduler SDK、Module 与 Runtime dispatch 已贯通 `business_action`。定义必须同时声明 Action、Object、JSON object payload 和 `run_as_role`；Runtime 只接受拥有精确 Action 权限的 `service + system_managed` Role，并将其解析为绑定当前 release lineage 的 workload principal。执行继续进入 Action Application Service，Scheduler window 形成稳定幂等键，不存在 Handler 直调或 system principal 降级。

### 5.5 [x] 建立文件字段语义

#### 实施前根本原因

当前上传授权只校验字段存在和字段写权限，因此任意业务字段都可能成为上传目标。Application Schema 没有 `file` 类型，记录中的文件引用缺少稳定类型合同。

#### 实施范围

- 增加封闭内建类型 `file` 和 `file_list`。
- 上传接口只允许文件类型字段。
- 字段配置声明允许 MIME、单文件大小、文件数量和扫描要求。
- Record 保存结构化 file reference，而不是无类型文件名或普通字符串。
- 下载、导出、生命周期删除、引用检查和审计复用同一个文件引用模型。

#### 验收标准

- 普通 `text` 字段不能上传文件。
- 上传、记录写入、下载和清理都校验 Workspace、Object、Field 和 file identity。
- `file_list` 的数量和去重约束在 Runtime 服务端执行。

#### 实施结果

`file`、`file_list` 已进入 Application Schema、Record normalize/validation、数据库 codec、查询限制、CSV/Data Exchange、Lifecycle 引用和 HTTP 上传/下载。文件引用是包含 immutable `file_id`、内容摘要、大小、媒体类型和扫描回执的结构值；普通字段不能成为上传目标，文件值只支持 null 查询，不支持搜索、排序、唯一或索引。

### 5.6 [x] 修正公开合同与执行能力不一致

以下问题属于缺陷，不应包装成新扩展：

- [x] Identity SDK 的 `contains` operator 已从公开支持合同删除；Runtime compiler 与 SDK operator 枚举一致并有覆盖测试。
- [x] Notification event/rule 引用未实现 Audience Resolver 时，在发布或启动阶段失败。
- [x] Data Exchange provider 注册返回错误、拒绝重复 key，并在全部内建 provider 完成组合后冻结。
- [x] Scheduler authoring 的公开字段全部进入 Definition 和执行；`run_as_role`、业务日历及 Business Action target 不再是空配置。

## 6. P1：核心缺陷完成后实施

### 6.1 [x] 声明式业务日历

#### 实施前根本原因

Workflow 已公开 `business_calendar_key`，但 Runtime 当前只硬编码 `24x7` 和 `weekday`。节假日、调休、轮班等正常业务无法被真实表达。

#### 实施范围

- 定义版本化 Business Calendar：
  - key；
  - timezone；
  - 每周工作区间；
  - 节假日；
  - 临时调休和例外日期；
  - revision。
- Workflow 发布时校验日历引用。
- Timer 创建时绑定 calendar revision 或等价不可变快照。
- 区分“增加工作时长”和“周期任务遇非工作日跳过/顺延”两种语义。
- SaaS Scheduler 如果消费业务日历，必须通过版本化协议传递，不能回调 Runtime 本地任意代码。

#### 不在范围内

- 项目提供任意 `AddBusinessDuration` Callback。
- 已创建 Timer 因日历后续修改而静默改变到期时间。

#### 实施结果

`schema.business_calendar` 已开放完整 authoring contract，定义由 key、revision、IANA timezone、每周半开工作区间、holiday 和 date exception 组成。Workflow 通过不可变 revision 增加工作时长；Scheduler 获得完整 snapshot，并对 calendar schedule 执行 `skip` 或 `roll_forward`。定义快照摘要进入 Scheduler revision，Module/SaaS 都不回调 Runtime 任意代码。

### 6.2 [x] 关系型数据权限 authoring

#### 实施前根本原因

项目 Role 当前只能选择 `all`、`owner`、`org`、`org_child` 和 `target_org`。这不能表达项目成员、区域负责人、客户团队等常见可见性规则，而 Identity SDK 与 Runtime 内部已经具备部分关系路径 Predicate 能力。

#### 实施范围

- 在 Identity SDK、Identity Module 和 Runtime Manifest 之间定义统一、强类型的 `data_policy` AST。
- 只允许 schema-bound 字段和有界 relation path。
- Subject value 只能来自 allowlist claim。
- 操作符必须是明确枚举，并有 Runtime query compiler 覆盖。
- 发布时同时由 Identity owner 和 Runtime schema owner 校验。

#### 不在范围内

- Go 权限回调。
- 原始 SQL 条件。
- 未经 schema 校验的 `map[string]any` Predicate。

#### 实施结果

Role 的每个 exact permission 现在必须二选一：内建 `data_scope` 或强类型 `data_policy`。AST 只允许 `and/or/not/eq/in`，叶子只能比较 schema-bound 字段与 allowlist subject claim；relation path 最多三段，拒绝环、禁用字段和目标不匹配。SDK 限制深度 8、节点 64；Identity owner 与 Runtime schema owner 都执行验证，Runtime query compiler 保持同一操作符语义。

### 6.3 [x] Blob Store 和 File Scanner 部署适配

#### 实施前根本原因

当前上传、派生文件、文件打开和扫描依赖本地目录及文件路径。它适合本地运行，但会限制多实例、容器和对象存储部署。

#### 实施范围

- Host 级 `BlobStore`：stage、commit、open、stat、delete。
- 独立 `FileScanner`：提交扫描、获取结果或流式扫描。
- key 必须包含 Workspace scope 和不可变内容 identity。
- stage 到 commit 需要原子 promotion 语义。
- local filesystem 作为默认 adapter，S3、OSS、MinIO 等由项目部署组合选择。
- Lifecycle 继续拥有引用、保留和删除证据。

这属于部署适配，不进入 `ProjectExtensions` 业务扩展注册表。

#### 实施结果

`runtimehost.Options.BlobStore` 与 `FileScanner` 已成为公开 host 组合合同；nil 使用 Runtime 的 local filesystem 与内建 scanner。`BlobStore` 提供 workspace-scoped stage、按内容 identity commit、open、stat、delete，LocalStore 以原子 link promotion 和摘要/大小复核关闭冲突。上传、下载、扫描与 Lifecycle 内容适配只依赖公开端口，不再传递本地绝对路径。adapter descriptor 属于部署身份，不进入项目业务扩展注册表。

### 6.4 [x] 按需求补充封闭字段类型

建议顺序：

1. `file`
2. `file_list`
3. `multi_select`
4. `json`

每种新字段类型必须一次覆盖：

- Authoring schema 和配置校验；
- Record normalize 和 validation；
- 三种数据库的 DDL、DML 与 row codec；
- filter、sort、index 和 comparison 语义；
- Report、Data Exchange 和导出；
- Schema change safety。

不得建设任意字段插件注册表。特别是 `json`，不能仅因 PostgreSQL/MySQL 存在 JSON 列映射就视为已经支持。

#### 实施结果与关闭语义

- `file`、`file_list`：结构化不可变引用；仅 null 查询；不搜索、排序、唯一或索引。
- `multi_select`：字符串集合，trim、去空、去重、排序后保存 canonical JSON；默认最多 100 项、上限 1000。只支持完整集合的 `eq/ne`，以及“完整集合属于候选集合列表”的 `in/not_in`，另支持 null；不提供成员 contains、搜索、排序、唯一或索引。
- `json`：默认 object，也可限定 array；默认 64 KiB、最大 1 MiB，另有深度 16 和节点 10,000 的固定保护。只支持 null 查询；不提供成员路径 filter、comparison、搜索、排序、唯一或索引。
- Report Object SQL 对 `multi_select` 和 `json` 都明确拒绝，不伪造跨方言集合或 JSON 查询语义。
- SQLite、PostgreSQL、MySQL 的 DDL/DML/row codec、backfill、CSV/Data Exchange canonical JSON 均已覆盖。任何 active field type 变化都由 upgrade planner 拒绝，即使两个类型碰巧使用相同物理列。

## 7. P2：有真实业务或部署需求时再实施

| 模块或能力 | 决策 | 启动条件 |
| --- | --- | --- |
| Notification 项目 Audience Resolver | 有条件开放 | Action 无法提前确定收件人，并且存在声明式事件源的真实动态找人需求 |
| Data Exchange 项目 Provider | 暂不开放 | 同时设计完提交、取消、下载、权限、artifact projection 和生命周期合同 |
| Monitoring Health Contributor | 有条件开放 | 项目扩展拥有独立外部依赖，并且该依赖真实影响 readiness |
| Project Lifecycle Participant | 暂不开放 | 项目扩展开始拥有独立持久状态或外部资源，而不是仅写 Runtime Records |
| Audit 远程拓扑 | 有条件实施 | 明确需要远程 Audit，并完成 outbox、receipt、reconciliation 和不可用策略设计 |
| Agent 自定义 Tool | 不开放通用接口 | 只有受治理 Record、Report、Action、Workflow 无法表达的具体只读能力出现时，增加专用 typed capability |
| Report 扩展 | 当前无需新增 | 现有 Dataset、Object SQL、AnalysisTableSourceFactory 无法覆盖明确需求时重新评估 |
| Integration/Connector | 当前无需新增 | 现有 Provider、Connection、Operation 和受控调用合同出现具体缺口时重新评估 |
| Metadata 模块拓扑 | 当前无需新增 | 出现明确的远程 Metadata owner 或独立部署要求时重新评估 |

## 8. 明确不做

以下接口会产生绕过 Runtime 治理的第二执行路径，当前不纳入路线：

- 通用 `RecordMutationPolicy` 或 CRUD Hook；
- 任意 Workflow Node 插件；
- 任意 Scheduler Target Executor；
- 用户上传脚本、SQL、Go 插件或运行时热替换；
- 任意物理字段类型插件；
- 扩展直接访问数据库、Repository 或裸事务；
- 扩展直接获得通用网络访问；
- 任意 Agent Tool 注册；
- 为未上线的旧入口增加 alias、双写或兼容层。

复杂状态迁移、跨记录校验和协调写入继续建模为 Business Operation。Workflow 负责等待和审批，Scheduler 负责周期调度，Notification 负责通知策略，Connector 负责外部协议，不能通过通用插件重新混合所有权。

## 9. 模块范围总表

| 所有者 | 当前结论 | 是否进入近期 TODO |
| --- | --- | --- |
| Runtimeext / Runtimehost | 项目扩展统一冻结并进入 release identity；部署 adapter 与业务扩展分离 | 已完成；不再扩成通用插件 |
| Workflow | 逐候选人证据、内建 resolver 与受控项目 resolver 已闭环 | 已完成 |
| Scheduler | Business Action、workload principal、业务日历 snapshot 已贯通 | 已完成 |
| Identity | operator 合同已收敛，关系型 `data_policy` 已由 SDK/Identity/Runtime 共同验证 | 已完成 |
| Upload / Lifecycle | 文件字段语义与 BlobStore/FileScanner 部署适配已闭环 | 已完成 |
| Notification | 未实现 Audience Resolver 引用已提前失败；项目 resolver 仍无普遍需求 | 核心完成；自定义 resolver 保持 P2 |
| Data Exchange | Provider registry 已冻结并拒绝冲突；结构化字段共享 Record codec | 核心完成；项目 Provider 保持 P2 |
| Integration / Connector | 已具备 descriptor、freeze、release hash 和受控 Operation | 否 |
| Report | 已有标准 Dataset 和 AnalysisTableSourceFactory | 否 |
| Agent | 业务写入应走 Action，不应开放任意 Tool | 否 |
| Monitoring | 暂无业务表达能力阻塞 | 条件性 P2 |
| Audit | 当前固定拓扑不是业务开发阻塞 | 条件性 P2 |
| Metadata | 当前固定拓扑不是业务开发阻塞 | 条件性 P2 |

## 10. 已完成的实施顺序

1. 统一 Project Extension 注册、冻结和发布 hash。
2. 修正 Workflow 候选人结果模型和内建 resolver 命名、语义。
3. 增加受控 Assignee Resolver。
4. 增加 Scheduler `business_action` 和真实 `run_as_role`。
5. 增加 `file`、`file_list` 字段语义。
6. 清理 Identity、Notification、Data Exchange、Scheduler 的公开合同不一致。
7. 增加声明式业务日历。
8. 增加关系型数据权限 authoring。
9. 抽象 Blob Store 和 File Scanner。
10. 补齐 `multi_select`、`json` 的全链路封闭语义。
11. 其余模块根据真实业务案例重新评审，不做预设性插件化。

## 11. 评审新扩展点的准入问题

以后提出新扩展接口时，必须先回答：

1. 现有 Record、Business Action、Workflow、Scheduler、Report 或 Connector 是否已经能真实表达？
2. 这是项目业务差异，还是部署基础设施差异？
3. 扩展能否只获得声明式或生成的窄能力？
4. 扩展行为能否通过 descriptor、freeze 和 release identity 固定？
5. 权限、Workspace、事务、幂等、审计和生命周期分别由谁拥有？
6. 失败是否能够关闭，而不是自动提升为 system 权限？
7. 是否至少存在一个当前项目无法开发的真实案例，而不是假设性灵活性？

任一问题没有清晰答案时，不开放扩展接口，先补齐所属模块的稳定合同。

## 12. 当前代码依据

本路线图的主要代码依据如下。后续相关代码发生结构性变化时，应同步复审本文结论。

| 判断 | 当前代码依据 |
| --- | --- |
| 项目扩展统一注册、冻结并生成 release hash | [`pkg/runtimeext/registry.go`](../../pkg/runtimeext/registry.go)、[`pkg/runtimehost/release_identity.go`](../../pkg/runtimehost/release_identity.go)、[`runtime/domain/deployment/model/deployment_runtime_release_identity.go`](../../runtime/domain/deployment/model/deployment_runtime_release_identity.go) |
| Workflow 每个候选人保留 Role、resolver 和 evidence | [`pkg/runtimeext/workflow_assignee.go`](../../pkg/runtimeext/workflow_assignee.go)、[`runtime/application/workflow/workflow_process_approval_application_service.go`](../../runtime/application/workflow/workflow_process_approval_application_service.go) |
| 内建及项目 Assignee Resolver 使用同一受控入口 | [`runtime/application/workflow/workflow_project_assignee_resolver.go`](../../runtime/application/workflow/workflow_project_assignee_resolver.go)、[`runtime/domain/workflow/policy/workflow_authoring_policy.go`](../../runtime/domain/workflow/policy/workflow_authoring_policy.go) |
| Scheduler Business Action、run_as_role 与 workload principal 已进入执行 | [`runtime/bootstrap/composition/scheduler_business_action_wiring.go`](../../runtime/bootstrap/composition/scheduler_business_action_wiring.go)、[`runtime/application/workflow/managed_workload_principal_application_service.go`](../../runtime/application/workflow/managed_workload_principal_application_service.go) |
| RunBusinessJob 仍是一次性 Record Timer 到 Action 的能力 | [`pkg/runtimeext/action_execution.go`](../../pkg/runtimeext/action_execution.go)、[`runtime/bootstrap/composition/action_application_wiring.go`](../../runtime/bootstrap/composition/action_application_wiring.go) |
| 文件上传只接受 file/file_list 并保存统一引用 | [`runtime/application/upload/upload_access_application_service.go`](../../runtime/application/upload/upload_access_application_service.go)、[`runtime/domain/record/model/record_file_reference.go`](../../runtime/domain/record/model/record_file_reference.go) |
| BlobStore/FileScanner 是 host 部署端口，本地 adapter 只是默认实现 | [`pkg/runtimefile/contract.go`](../../pkg/runtimefile/contract.go)、[`pkg/runtimehost/options.go`](../../pkg/runtimehost/options.go)、[`runtime/infrastructure/blobstore/local_store.go`](../../runtime/infrastructure/blobstore/local_store.go) |
| Application Schema 已披露 file、file_list、multi_select、json 的配置和错误 | [`runtime/domain/appschema/contract/appschema_field_authoring.go`](../../runtime/domain/appschema/contract/appschema_field_authoring.go)、[`runtime/domain/appschema/contract/appschema_authoring_schema.go`](../../runtime/domain/appschema/contract/appschema_authoring_schema.go) |
| Record 已提供结构化字段 canonical normalize、query 和 row codec | [`runtime/domain/record/model/record_structured_field.go`](../../runtime/domain/record/model/record_structured_field.go)、[`runtime/domain/record/validation/record_query_validation.go`](../../runtime/domain/record/validation/record_query_validation.go)、[`runtime/infrastructure/persistence/database/record/record_row_codec.go`](../../runtime/infrastructure/persistence/database/record/record_row_codec.go) |
| 版本化业务日历同时服务 Workflow 工作时长与 Scheduler 非工作日策略 | [`runtime/domain/businesscalendar`](../../runtime/domain/businesscalendar)、[`runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go`](../../runtime/bootstrap/composition/scheduler_sdk_module_host_wiring.go) |
| 关系型数据权限由 Identity SDK AST 与 Runtime schema path 双重约束 | [`runtime/domain/manifest/validation/manifest_role_authorization_validation.go`](../../runtime/domain/manifest/validation/manifest_role_authorization_validation.go)、`github.com/domainry/domainry-identity-sdk/authorization/project_roles.go` |
| Notification Audience Resolver host inventory 与 Data Exchange registry 均提前校验并冻结 | [`runtime/domain/hostsurface/model/notification_audience_resolvers.go`](../../runtime/domain/hostsurface/model/notification_audience_resolvers.go)、[`runtime/domain/manifest/validation/manifest_notification_audience_resolvers.go`](../../runtime/domain/manifest/validation/manifest_notification_audience_resolvers.go)、[`runtime/application/record/record_data_exchange_providers.go`](../../runtime/application/record/record_data_exchange_providers.go) |
| Runtime authoring JSON 由代码生成、规范化、排序并用固定 hash 防漂移 | [`runtime/application/capability/capability_authoring_catalog.go`](../../runtime/application/capability/capability_authoring_catalog.go)、[`runtime/application/capability/capability_authoring_contract_test.go`](../../runtime/application/capability/capability_authoring_contract_test.go) |
| 普通 CRUD 与 Business Operation 的责任边界 | [`capability/agent/records.md`](../../capability/agent/records.md)、[`capability/agent/business-operations.md`](../../capability/agent/business-operations.md) |

## 13. 公开合约一致性审计

### 13.1 合同源

Runtime 没有一份手工维护的完整 authoring JSON。权威 JSON 由 `RuntimeAuthoringCapabilities()` 从代码生成，经规范化和稳定排序后由 discovery 接口返回；`RuntimeAuthoringContractHash` 是发布锁。本次完成后的 canonical hash 为：

```text
f04fef01b6adefdde3abbd0f3b48196951fb64ecdb6db3195b8296fb649f49f7
```

当前 catalog 共 23 个 Runtime-owned capability，分属 `schema`、`action`、`workflow`、`automation`、`principal`、`maintenance` 六个 domain。Scheduler、Identity、Lifecycle 等模块的 authoring capability 仍由各自 source owner 发布，Runtime 的 guide index 可以引用它们，但不能复制或冒充它们的 schema。

`capability/agent/index.json` 是按业务问题选择 Markdown 指南的静态路由，不是 authoring schema。它与动态 catalog 的职责不同，不能用其中的少量 `capability_keys` 推断完整输入字段。

### 13.2 本次发现并修正的漂移

| 漂移 | 修正 |
| --- | --- |
| `schema.field` 已支持文件与结构化字段，但 Records 指南只描述文件 | 补齐 `multi_select`、`json` 的配置、canonical value、查询、Report、Data Exchange 和 schema-change 边界 |
| Records 示例仍用字符串数组表达 field `options` | 改为 direct authoring 实际接受的 `{value,label}` 稳定选项 |
| `schema.business_calendar` 已在 catalog 中，但 guide index 没有选型入口 | 新增 Business Calendar 指南，并从 Workflow、Automation 互链 |
| Scheduler 已支持 `business_action`、`run_as_role` 和 calendar snapshot，但模块指南只描述旧目标 | 同步 Runtime Scheduler 文档和 Scheduler source-owned agent guide/index |
| Identity/Runtime 已支持关系型 `data_policy`，Identity 权限指南仍声称只有五种固定 scope | 补齐强类型 AST、relation path、subject claim 和双 owner 校验边界 |
| guide index 的 Runtime capability key 与文件路径没有自动校验 | 新增测试：解析 index、验证路径、related guide、Runtime-owned key，并固定本轮关键披露项 |

### 13.3 后续防漂移规则

1. 修改任何 authoring capability 的字段、错误、reference、example 或 execution contract，必须更新固定 hash；否则 capability test 失败。
2. guide index 新增 Runtime-owned capability key 时，必须在动态 catalog 中真实存在；拼写或删除漂移会由测试拒绝。
3. 新字段类型必须在同一变更中更新 field authoring schema、Records 指南和端到端语义测试，不能只改数据库映射。
4. 模块能力由 source owner 的 JSON/Markdown 负责，Runtime 只维护组合事实和跨 owner 边界；两边发生协议变化时必须同批修改。
5. 静态指南只解释选择与边界；字段级事实始终以锁定 release 的动态 authoring JSON 为准。

## 14. 验证与发布边界

本轮已经通过以下验证：

- Runtime 全包编译：`go test ./... -run '^$'`。
- Runtime authoring/discovery、Manifest、架构边界、Workflow、Upload、Business Calendar、BlobStore、Record、AppSchema、Report 的相关完整 package tests。
- 使用仓库 `go.work` 组合当前源码后，Identity、Scheduler、Lifecycle 及三个 SDK 的 `go test ./...`。
- 所有 11 个依赖模块的 `capability/agent/index.json` 可解析、guide key 不重复、Markdown 路径存在；其中声明的 capability key 均能在各自 source owner 仓库找到来源。
- Runtime guide index 与动态 Runtime capability key 的自动一致性测试。
- 本次重点包的 `go test -race` 与 `go vet`，覆盖 Runtimeext、Workflow、Scheduler composition、Upload、BlobStore、Business Calendar、Record、Record Timer、Action 和 Capability。

完整 Runtime `go test ./...` 仍会被三项本路线之外的现存集成基线阻断：Agent SDK 已在 `go.mod` 选择 `v0.1.18`、module-set lock 仍是 `v0.1.17`；Agent owner 声明了 `agent.task_runs.start` 但没有 mounted adapter route；外部生成项目选择 Knowledge `v0.1.5` 而 Runtime 仍锁 `v0.1.4`。这些问题没有通过放宽测试隐藏，也不属于本次扩展边界实现。

Identity、Scheduler、Lifecycle 的模块源码现在依赖同批修改的 SDK 源码，所以本地联合验证使用 `go.work`。正式发布时必须按 SDK → Module → Runtime 的顺序发布版本、更新各模块 `go.mod/go.sum`，最后刷新 Runtime module-set lock；不应把本地 `replace` 写入发布合同。

## 15. 二次边界验收

本节记录 2026-09-21 在首轮实现完成后的独立复核。复核重点不是继续增加扩展范围，而是验证已开放边界在异常输入、并发重试、时区变化和契约漂移下仍然失败关闭。

### 15.1 复核发现与修正

| 边界 | 根本原因 | 修正后的关闭语义 |
| --- | --- | --- |
| Business Calendar DST | 工作区间曾用“本地零点 + 小时数”构造；DST 切换日会把 09:00 漂移成 08:00 或 10:00 | 用目标日期的本地墙上时钟直接构造区间；覆盖春季跳时和秋季回拨。负向 `time.Duration` 的最小值也不再因取反溢出 |
| Local BlobStore | `Commit` 的“幂等”测试只覆盖重新 stage，没有覆盖同一次 Commit 的精确重放；路径打开也缺少 symlink/非普通文件拒绝 | 精确 Commit 重放成功；stage key、SHA256、大小和最终内容一致性全部校验；所有文件操作限制在 workspace root，symlink/非普通文件失败关闭；精确大小上限可接受，超一字节和取消均拒绝 |
| 派生文件并发幂等 | 最终 file identity 与临时 stage identity 共用了同一稳定 key，并发相同幂等请求会在临时阶段冲突，异常残留也会阻断重试 | 最终 `file_id/blob key` 保持稳定，stage 每次使用独立随机 nonce；并发相同请求最终返回同一文件身份和扫描回执 |
| 结构化字段 | `multi_select.max_items` 曾在去重前计算；`config.indexed` 非布尔值会被静默当成 false；文件 codec 未统一拒绝尾随第二个 JSON 值 | 数量按 canonical 去重集合计算；`indexed` 必须是布尔；string/bytes/in-memory file value 统一规范化并且只接受一个完整 JSON 值 |
| Assignee Resolver 配置与结果 | JSON 整数曾可能经 binary float 转换而丢精度；规范化后重复 config key、同字段重复 filter 没有明确拒绝；候选人只校验“允许返回该 Role”，没有校验用户真实拥有该 Role | 整数沿用 Runtime 的精确 int64 规范化并拒绝不安全 float；重复 key/filter 失败关闭；带 Role 的候选人必须是该 Identity Role 的当前 active member |
| Scheduler `business_action` | dispatch payload 只做普通反序列化，可能丢失大整数精度，也没有统一大小与尾随值约束；authoring 整数字段会接受字符串或截断小数 | SDK 与 Runtime 共用 64 KiB 上限；payload 只接受恰好一个 JSON object，并用 `json.Number` 保留数值；缺少 execution identity、数组、尾随 JSON、超限载荷均在调用 principal/Action 前拒绝；整数不再做字符串或小数强转 |
| 一次性 `RunBusinessJob` / Record Timer | 扩展入口只用 `json.Valid`，通用 Timer 创建策略甚至不校验 payload；错误或无界载荷会先持久化，到 worker 消费时才失败 | 两层都只接受 JSON object 且上限 64 KiB；Record Timer 在持久化前拒绝数组、null、损坏和超限载荷；重试边界同时在扩展入口提前校验 |
| 文件主体授权 | 非 system principal 携带 claim 调用 `VerifyClean` 时，未绑定 subject registry 会 nil dereference | 缺少 registry 明确返回 `backend.upload.subject_binding_denied`，不再崩溃或绕过授权 |
| 公开合同与静态审计 | 新增 `indexed_invalid` 错误改变了 canonical authoring catalog；BlobStore 新增 workspace 空值守卫改变了静态 review 清单 | 根据真实生成结果更新 `RuntimeAuthoringContractHash`；逐条确认五个 BlobStore 守卫都只拒绝空 workspace、不提供默认 workspace 后更新 baseline |

### 15.2 边界用例矩阵

已增加或重新执行的重点用例包括：

- DST 前进/回拨、本地工作区间、关闭未来搜索耗尽、`time.Duration` 最小值。
- Blob stage 精确上限、超限、取消、非法 stage key、symlink、内容冲突、Commit 精确重放。
- 派生文件串行和并发相同幂等键、相同键不同内容冲突、已存内容摘要复核。
- `multi_select` trim/去重后的数量上限；JSON/file codec 的 string、bytes、内存值和尾随 JSON。
- Resolver config 的精确大整数、越界、不安全 float、小数、非有限数、规范化重复 key；候选 Role 未实际分配。
- Scheduler payload 的 64 KiB 边界、超限、数组、尾随值、缺少 execution ID 和超过 JavaScript 安全整数的精确值。
- Record Timer payload 的恰好 64 KiB、数组、null、损坏与超限。

### 15.3 二次验收结果

- Runtime 全仓 `go test ./... -run '^$'` 通过，说明所有 package 可编译。
- 上述重点 Runtime package 的普通测试、Race Detector 和 Vet 全部通过。
- Scheduler SDK 全包测试、Scheduler Module 全包测试，以及 Identity/Lifecycle 的 SDK 与 Module 全包测试在当前源码 `go.work` 下全部通过。
- 完整 Runtime `go test ./...` 除第 14 节列出的三类既有跨仓版本/路由基线外，其余 package 通过；本轮触发的 authoring hash 与 workspace fallback baseline 已修复并单独复测通过。

### 15.4 复核后的范围判断

本次复核没有发现需要继续扩大扩展面的证据。当前应继续坚持：项目算法只进入受控 Assignee Resolver 或 Business Handler；周期执行只进入 Scheduler `business_action`；文件存储差异只进入 host adapter；结构化字段保持封闭类型。剩余三类全仓失败应在对应 Agent/Knowledge 发布批次修复，不能通过放宽 Runtime 启动校验或删除合同测试来掩盖。
