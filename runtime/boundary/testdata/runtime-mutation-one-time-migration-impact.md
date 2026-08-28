# Runtime Mutation 一次性迁移影响清单

> 状态：2026-07-21 已执行。仓库尚未生产上线，本清单只记录需要同步升级或删除的 repo-owned 定义、fixture、executor、validator 与测试，不定义运行时兼容策略。

| 能力 | 新的唯一合同 | 一次性升级范围 | 已删除的重复 owner | 可执行证据 |
|---|---|---|---|---|
| Source-owned Action mutation / atomic batch | generated typed mutation capabilities + `ActionUnitOfWork` | generated data/capability bindings、project Handler tests | Runtime-owned Action Step wiring、atomic batch interpreter 和 direct committer | generated SDK mutation tests + Action UoW transaction tests |
| Workflow business mutation | published Business Action | Workflow typed action node、approval reminder、CC notification | Workflow Record CRUD/commit executor | `TestWorkflowBusinessMutationUsesPublishedActionOnly` |
| before Automation | 纯 `derive_fields` / `assert` planning hook | automation validator、Manifest validator、capability/authoring catalog；CRM fixture 的 before action 调用迁到 after | 通用 effect dispatcher、before audit/execution history、Action/Workflow/Event/Connector I/O | `TestAutomationPhaseBoundaryHasNoPreCommitEffectFallback` |
| after Automation | committed lifecycle Outbox consumer | lifecycle envelope、identity policy、correlation/causation、depth/visited chain、CRM after fixture | 无 identity revalidation 或 loop/depth gate 的消费路径 | `TestAutomationAfterOutboxRevalidatesIdentityAndRejectsLoopsAndDepth` |
| State Machine | transition legality + 纯 self patch，随父 Record `MutationPlan` 提交 | Domain/Manifest validator、Record/Action candidate planning、状态机测试 | `create_record`、`update_related`、Workflow、Audit post-commit effect executor | `TestStateMachineOwnsOnlyPureTransitionPlanning` |
| Pipeline Transition | typed stage projection + canonical Record item/history plans | Pipeline wiring、failure-window fixture、composition tests | Pipeline 自有 RunBefore、AfterOutbox、Record validators、Audit/Workflow preparation、repository committer | `TestPipelineTransitionDelegatesRecordSemanticsToCanonicalPlanner` |
| Import/Batch、Integration/Agent | canonical Record facade | mutation entrypoint inventory 与 boundary test | 未发现需要保留的直写 executor | `TestNonInteractiveMutationEntrypointsHaveExplicitCanonicalOrInternalDecision` |
| Scheduler / record timer / lifecycle / installation seed | 明确登记的 audited 或 installation internal owner | Scheduler object allowlist、record timer schedule/claim/finish/fail/cancel、identity/business seed 和 subject lifecycle inventory | 未登记 internal repository write | `TestRuntimeApplicationHasNoUnregisteredRecordRepositoryWriteEntrypoint` |

## 删除准则

- 不接受 `legacy`、fallback、双 validator、双 committer 或按旧 Definition 分支执行。
- repo fixture、Manifest、capability catalog、测试和生成物必须与新合同在同一变更升级。
- 被新 owner 替代的代码必须物理删除；仅从 production wiring 绕开不算完成。
- 已提交的异步事实只从 durable Outbox/Intent 恢复，不实现“兼容旧副作用”的重放器。
