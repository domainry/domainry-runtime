package workflow

import (
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func workflowTaskNotificationEvent(id, sourceID string) notificationmodel.NotificationEvent {
	return notificationmodel.NotificationEvent{
		ID: id, WorkspaceID: "workspace-a", Source: "workflow", SourceEventID: sourceID, EventType: "workflow.task.assigned", Category: "approval", Severity: "info", Surface: "business_workspace",
		RecipientUserIDs: []string{"manager"}, SubjectType: "workflow_task", SubjectID: "task-1", ActionState: "open", OccurredAt: "2026-07-28T00:00:00Z",
		Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Approval required", Body: "Please review", TemplateKey: "workflow.task.assigned.in_app", TemplateVersion: 1, TemplateLocale: "en-US", TemplateContentHash: "hash", Actions: []notificationmodel.NotificationInboxActionRef{{Key: "workflow.task.open", Kind: "route", Label: "Review task", ResourceType: "workflow_task", ResourceID: "task-1"}}},
		Status:   "queued", CreatedAt: "2026-07-28T00:00:00Z", UpdatedAt: "2026-07-28T00:00:00Z",
	}
}

func workflowTaskNotificationTask(id, status string) workflowmodel.WorkflowTask {
	return workflowmodel.WorkflowTask{ID: id, ProcessID: "process-1", NodeInstanceID: "node-1", NodeID: "approve", Title: "Approve", AssigneeUserID: "manager", Sequence: 1, Status: status, CreatedAt: "2026-07-28T00:00:00Z", UpdatedAt: "2026-07-28T00:00:00Z"}
}

func TestWorkflowTaskAndNotificationCommitOrRollbackTogether(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	committer := NewWorkflowTaskNotificationStore(store)
	processes := NewWorkflowProcessStore(store)
	if err := committer.CommitWorkflowTaskNotification(t.Context(), "workspace-a", workflowTaskNotificationTask("task-1", "open"), workflowTaskNotificationEvent("notification-1", "task-1:assigned")); err != nil {
		t.Fatal(err)
	}
	if task, found, err := processes.GetTask(t.Context(), "workspace-a", "task-1"); err != nil || !found || task.Status != "open" {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	var eventCount int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier("_notification_events")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), "workspace-a").Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("event count=%d err=%v", eventCount, err)
	}

	// The duplicate source identity makes notification insertion fail after the
	// task insert attempt; the transaction must roll the task back.
	duplicate := workflowTaskNotificationEvent("notification-2", "task-1:assigned")
	if err := committer.CommitWorkflowTaskNotification(t.Context(), "workspace-a", workflowTaskNotificationTask("task-rollback", "open"), duplicate); err == nil {
		t.Fatal("expected duplicate notification source failure")
	}
	if _, found, err := processes.GetTask(t.Context(), "workspace-a", "task-rollback"); err != nil || found {
		t.Fatalf("rolled back task found=%v err=%v", found, err)
	}

	pending := workflowTaskNotificationTask("task-pending", "pending")
	if err := processes.InsertTask(t.Context(), "workspace-a", pending); err != nil {
		t.Fatal(err)
	}
	pending.Status, pending.UpdatedAt = "open", "2026-07-28T01:00:00Z"
	opening := workflowTaskNotificationEvent("notification-opening", "task-pending:assigned")
	opening.SubjectID, opening.Snapshot.Actions[0].ResourceID = pending.ID, pending.ID
	if err := committer.CommitWorkflowTaskOpeningNotification(t.Context(), "workspace-a", pending, opening); err != nil {
		t.Fatal(err)
	}
	if task, found, err := processes.GetTask(t.Context(), "workspace-a", pending.ID); err != nil || !found || task.Status != "open" {
		t.Fatalf("opened task=%+v found=%v err=%v", task, found, err)
	}

}

func TestWorkflowTaskEscalationTaskEventAndBothNotificationsAreAtomic(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	committer := NewWorkflowTaskNotificationStore(store)
	processes := NewWorkflowProcessStore(store)
	task := workflowTaskNotificationTask("task-escalation", "open")
	if err := processes.InsertTask(t.Context(), "workspace-a", task); err != nil {
		t.Fatal(err)
	}
	task.AssigneeUserID, task.UpdatedAt = "director", "2026-07-28T02:00:00Z"
	processEvent := workflowmodel.WorkflowProcessEvent{ID: "event-escalated", ProcessID: task.ProcessID, NodeID: task.NodeID, TaskID: task.ID, Event: "task_escalated", ActorID: "system", Summary: "workflow.task.escalated", Metadata: map[string]any{"previous_assignee_user_id": "manager", "assignee_user_id": "director"}, CreatedAt: task.UpdatedAt}
	oldEvent := workflowTaskNotificationEvent("notification-escalation-old", "task-escalation:reassigned-from:manager")
	oldEvent.RecipientUserIDs, oldEvent.SubjectID = []string{"manager"}, task.ID
	newEvent := workflowTaskNotificationEvent("notification-escalation-new", "task-escalation:assigned-to:director")
	newEvent.RecipientUserIDs, newEvent.SubjectID = []string{"director"}, task.ID
	if err := committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", task, processEvent, []notificationmodel.NotificationEvent{oldEvent, newEvent}); err != nil {
		t.Fatal(err)
	}
	updated, found, err := processes.GetTask(t.Context(), "workspace-a", task.ID)
	if err != nil || !found || updated.AssigneeUserID != "director" {
		t.Fatalf("updated=%+v found=%v err=%v", updated, found, err)
	}
	var processEvents, notifications int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _workflow_process_events WHERE workspace_id = ? AND task_id = ?", "workspace-a", task.ID).Scan(&processEvents); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _notification_events WHERE workspace_id = ?", "workspace-a").Scan(&notifications); err != nil || processEvents != 1 || notifications != 2 {
		t.Fatalf("process events=%d notifications=%d err=%v", processEvents, notifications, err)
	}

	rollbackTask := workflowTaskNotificationTask("task-escalation-rollback", "open")
	if err := processes.InsertTask(t.Context(), "workspace-a", rollbackTask); err != nil {
		t.Fatal(err)
	}
	rollbackTask.AssigneeUserID = "director"
	duplicate := workflowTaskNotificationEvent("notification-escalation-duplicate", oldEvent.SourceEventID)
	if err := committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", rollbackTask, workflowmodel.WorkflowProcessEvent{ID: "event-rollback", ProcessID: rollbackTask.ProcessID, TaskID: rollbackTask.ID, Event: "task_escalated", ActorID: "system", Summary: "workflow.task.escalated", CreatedAt: rollbackTask.UpdatedAt}, []notificationmodel.NotificationEvent{duplicate}); err == nil {
		t.Fatal("expected duplicate notification source failure")
	}
	rolledBack, found, err := processes.GetTask(t.Context(), "workspace-a", rollbackTask.ID)
	if err != nil || !found || rolledBack.AssigneeUserID != "manager" {
		t.Fatalf("rolled back task=%+v found=%v err=%v", rolledBack, found, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _workflow_process_events WHERE workspace_id = ? AND id = ?", "workspace-a", "event-rollback").Scan(&processEvents); err != nil || processEvents != 0 {
		t.Fatalf("rolled back process events=%d err=%v", processEvents, err)
	}
}

func TestWorkflowTaskNotificationValidationBeginAndStageFailures(t *testing.T) {
	task := workflowTaskNotificationTask("task", "open")
	event := workflowTaskNotificationEvent("notification", "task:assigned")
	processEvent := workflowmodel.WorkflowProcessEvent{ID: "process-event", ProcessID: task.ProcessID, TaskID: task.ID, Event: "task_reminder", CreatedAt: task.CreatedAt}

	store := openStoreForGeneratedListTest(t)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	committer := NewWorkflowTaskNotificationStore(store)
	for _, call := range []func(string) error{
		func(workspace string) error {
			return committer.CommitWorkflowTaskNotification(t.Context(), workspace, task, event)
		},
		func(workspace string) error {
			return committer.CommitWorkflowTaskOpeningNotification(t.Context(), workspace, task, event)
		},
		func(workspace string) error {
			return committer.CommitWorkflowTaskReminderNotification(t.Context(), workspace, processEvent, event)
		},
		func(workspace string) error {
			return committer.CommitWorkflowTaskEscalationNotification(t.Context(), workspace, task, processEvent, nil)
		},
	} {
		if err := call(" "); err == nil {
			t.Fatal("invalid workspace accepted")
		}
	}

	processes := NewWorkflowProcessStore(store)
	if err := processes.InsertTask(t.Context(), "workspace-a", task); err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitWorkflowTaskReminderNotification(t.Context(), "workspace-a", processEvent, event); err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitWorkflowTaskReminderNotification(t.Context(), "workspace-a", processEvent, workflowTaskNotificationEvent("other-notification", "other-source")); err == nil {
		t.Fatal("duplicate reminder process event accepted")
	}
	secondProcessEvent := processEvent
	secondProcessEvent.ID = "second-process-event"
	if err := committer.CommitWorkflowTaskReminderNotification(t.Context(), "workspace-a", secondProcessEvent, workflowTaskNotificationEvent("duplicate-source", event.SourceEventID)); err == nil {
		t.Fatal("duplicate reminder notification accepted")
	}
	missing := task
	missing.ID = "missing"
	if err := committer.CommitWorkflowTaskOpeningNotification(t.Context(), "workspace-a", missing, workflowTaskNotificationEvent("missing-task", "missing-task:assigned")); err == nil {
		t.Fatal("missing opening task accepted")
	}
	if err := committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", missing, workflowmodel.WorkflowProcessEvent{ID: "missing-escalation"}, nil); err == nil {
		t.Fatal("missing escalation task accepted")
	}
	duplicateEscalationEvent := processEvent
	duplicateEscalationEvent.ID = "process-event"
	if err := committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", task, duplicateEscalationEvent, nil); err == nil {
		t.Fatal("duplicate escalation process event accepted")
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return committer.CommitWorkflowTaskNotification(t.Context(), "workspace-a", task, event) },
		func() error {
			return committer.CommitWorkflowTaskOpeningNotification(t.Context(), "workspace-a", task, event)
		},
		func() error {
			return committer.CommitWorkflowTaskReminderNotification(t.Context(), "workspace-a", processEvent, event)
		},
		func() error {
			return committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", task, processEvent, nil)
		},
	} {
		if err := call(); err == nil {
			t.Fatal("closed database transaction began")
		}
	}
}

func TestWorkflowTaskNotificationDeferredCommitFailures(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE gate_parent (id TEXT PRIMARY KEY)`,
		`CREATE TABLE gate_child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES gate_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	committer := NewWorkflowTaskNotificationStore(store)
	task := workflowTaskNotificationTask("commit-task", "open")
	event := workflowTaskNotificationEvent("commit-notification", "commit-task:assigned")

	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_task_commit AFTER INSERT ON _workflow_tasks BEGIN INSERT INTO gate_child VALUES (NEW.id, 'missing'); END`); err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitWorkflowTaskNotification(t.Context(), "workspace-a", task, event); err == nil {
		t.Fatal("deferred task notification commit failure ignored")
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER fail_task_commit`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_event_commit AFTER INSERT ON _workflow_process_events BEGIN INSERT INTO gate_child VALUES (NEW.id, 'missing'); END`); err != nil {
		t.Fatal(err)
	}
	processEvent := workflowmodel.WorkflowProcessEvent{ID: "commit-reminder-event", ProcessID: task.ProcessID, TaskID: task.ID, Event: "task_reminder", CreatedAt: task.CreatedAt}
	if err := committer.CommitWorkflowTaskReminderNotification(t.Context(), "workspace-a", processEvent, workflowTaskNotificationEvent("commit-reminder", "commit-reminder")); err == nil {
		t.Fatal("deferred reminder commit failure ignored")
	}
	processes := NewWorkflowProcessStore(store)
	if err := processes.InsertTask(t.Context(), "workspace-a", task); err != nil {
		t.Fatal(err)
	}
	processEvent.ID = "commit-escalation-event"
	if err := committer.CommitWorkflowTaskEscalationNotification(t.Context(), "workspace-a", task, processEvent, nil); err == nil {
		t.Fatal("deferred escalation commit failure ignored")
	}
}
