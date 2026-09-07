# Workflow 审批模式

审批节点的 `contract.approval.mode` 支持 `any`、`all`、`sequential` 和 `quorum`。

`quorum` 表示 N 人中 K 人通过。N 是节点启动时经过现有 resolver 解析并去重后的实际审批人数，K 由 `required_approvals` 指定：

```json
{
  "mode": "quorum",
  "required_approvals": 2,
  "resolvers": [
    { "type": "users", "user_ids": ["user-1", "user-2", "user-3"] }
  ]
}
```

- 所有审批任务同时开放；第二人通过后节点通过，剩余未完成任务取消。
- `required_approvals` 必须为正整数，只能在 `quorum` 模式使用。运行和模拟时还会检查 K 不超过实际人数；不足时报告配置错误，不会通过 `empty_assignee_policy: skip` 绕过阈值。
- 拒绝和退回沿用现有审批语义：任一人拒绝或退回立即结束该节点，按对应分支继续。
- 定义快照保存模式与阈值；计票只使用本次节点实例的完整任务集合，不受待办列表分页影响。
- 决策接口保持不变。携带相同幂等键的重试不会重复计票；并发投票通过流程版本条件更新重新计算结算结果，任务、节点、流程、后续待办及事件仍在现有事务边界提交。

任务持久化复用现有表，无新增迁移。自定义 Workflow 存储实现若启用 `quorum`，需实现 `WorkflowApprovalTaskReader`，并在 `CommitWorkflowDecision` 中执行 `ExpectedProcessUpdatedAt` 条件更新；快照冲突返回 `ErrWorkflowDecisionSnapshotChanged`。
