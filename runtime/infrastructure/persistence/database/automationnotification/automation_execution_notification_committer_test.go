package automationnotification

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

var _ automationapplication.AutomationExecutionNotificationCommitter = AutomationExecutionNotificationCommitter{}

func TestAutomationExecutionOnlyCommitIsReplaySafe(t *testing.T) {
	store := openAutomationNotificationStore(t)
	defer store.Close()
	committer := NewAutomationExecutionNotificationCommitter(store)
	execution := automationNotificationExecution("execution-only")
	if err := committer.CommitAutomationExecution(t.Context(), execution); err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitAutomationExecution(t.Context(), execution); err != nil {
		t.Fatalf("execution replay failed: %v", err)
	}
	assertAutomationNotificationCounts(t, store, execution.ID, 1, 0)
}

func TestAutomationExecutionAndNotificationAreAtomicAndReplaySafe(t *testing.T) {
	store := openAutomationNotificationStore(t)
	defer store.Close()
	committer := NewAutomationExecutionNotificationCommitter(store)

	execution := automationNotificationExecution("execution-1")
	event := automationNotificationEvent("notification-1", "automation:execution-1:completed")
	if err := committer.CommitAutomationExecutionNotification(t.Context(), execution, event); err != nil {
		t.Fatal(err)
	}
	assertAutomationNotificationCounts(t, store, execution.ID, 1, 1)
	if err := committer.CommitAutomationExecutionNotification(t.Context(), execution, event); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	assertAutomationNotificationCounts(t, store, execution.ID, 1, 1)

	rollbackExecution := automationNotificationExecution("execution-rollback")
	primaryKeyConflict := automationNotificationEvent(event.ID, "automation:execution-rollback:completed")
	if err := committer.CommitAutomationExecutionNotification(t.Context(), rollbackExecution, primaryKeyConflict); err == nil {
		t.Fatal("expected notification primary-key conflict")
	}
	assertAutomationNotificationCounts(t, store, rollbackExecution.ID, 0, 1)
}

func TestAutomationExecutionNotificationErrorAndConsistencyEdges(t *testing.T) {
	t.Run("invalid execution", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		defer store.Close()
		committer := NewAutomationExecutionNotificationCommitter(store)
		execution := automationNotificationExecution("invalid-execution")
		execution.Candidate = map[string]any{"bad": make(chan int)}
		if err := committer.CommitAutomationExecution(t.Context(), execution); err == nil {
			t.Fatal("invalid execution was committed")
		}
	})

	t.Run("inconsistent execution only", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		defer store.Close()
		committer := NewAutomationExecutionNotificationCommitter(store)
		execution := automationNotificationExecution("execution-inconsistent")
		if err := committer.CommitAutomationExecution(t.Context(), execution); err != nil {
			t.Fatal(err)
		}
		if _, err := committer.committed(t.Context(), execution, automationNotificationEvent("unused", "missing")); err == nil {
			t.Fatal("inconsistent execution-only state was accepted")
		}
	})

	t.Run("event query error", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		defer store.Close()
		committer := NewAutomationExecutionNotificationCommitter(store)
		if _, err := store.DB().ExecContext(t.Context(), `DROP TABLE _notification_events`); err != nil {
			t.Fatal(err)
		}
		if _, err := committer.committed(t.Context(), automationNotificationExecution("query-error"), automationNotificationEvent("event", "source")); err == nil {
			t.Fatal("notification query error was ignored")
		}
	})

	t.Run("closed store", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		committer := NewAutomationExecutionNotificationCommitter(store)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		execution := automationNotificationExecution("closed")
		if err := committer.CommitAutomationExecution(t.Context(), execution); err == nil {
			t.Fatal("closed execution store was accepted")
		}
		if err := committer.CommitAutomationExecutionNotification(t.Context(), execution, automationNotificationEvent("closed", "closed")); err == nil {
			t.Fatal("closed notification store was accepted")
		}
	})
}

func TestAutomationExecutionNotificationDeferredCommitFailure(t *testing.T) {
	store := openAutomationNotificationStore(t)
	defer store.Close()
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE automation_gate_parent (id TEXT PRIMARY KEY)`,
		`CREATE TABLE automation_gate_child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES automation_gate_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_automation_notification_commit AFTER INSERT ON _notification_events BEGIN INSERT INTO automation_gate_child VALUES (NEW.id, 'missing'); END`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	committer := NewAutomationExecutionNotificationCommitter(store)
	if err := committer.CommitAutomationExecutionNotification(t.Context(), automationNotificationExecution("commit-failure"), automationNotificationEvent("commit-failure", "commit-failure")); err == nil {
		t.Fatal("deferred commit failure was ignored")
	}
}

func TestAutomationExecutionNotificationRaceRecoveryAndTransactionEdges(t *testing.T) {
	t.Run("execution retry inspection", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			found bool
			err   error
		}{
			{name: "inspection error", err: errors.New("inspect failed")},
			{name: "concurrent commit", found: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				store := openAutomationNotificationStore(t)
				defer store.Close()
				committer := NewAutomationExecutionNotificationCommitter(store)
				calls := 0
				committer.inspectExecution = func(context.Context, automationmodel.AutomationRuleExecution) (bool, error) {
					calls++
					if calls == 1 {
						return false, nil
					}
					return test.found, test.err
				}
				execution := automationNotificationExecution("retry-" + test.name)
				execution.Candidate = map[string]any{"bad": make(chan int)}
				err := committer.CommitAutomationExecution(t.Context(), execution)
				if test.found && err != nil {
					t.Fatalf("concurrent commit was not recovered: %v", err)
				}
				if !test.found && !errors.Is(err, test.err) {
					t.Fatalf("error=%v want=%v", err, test.err)
				}
			})
		}
	})

	t.Run("begin error", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		defer store.Close()
		committer := NewAutomationExecutionNotificationCommitter(store)
		committer.inspectCommit = func(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) (bool, error) {
			return false, nil
		}
		committer.beginTx = func(context.Context) (*sql.Tx, error) { return nil, errors.New("begin failed") }
		if err := committer.CommitAutomationExecutionNotification(t.Context(), automationNotificationExecution("begin"), automationNotificationEvent("begin", "begin")); err == nil {
			t.Fatal("begin error was ignored")
		}
	})

	t.Run("execution insert error", func(t *testing.T) {
		store := openAutomationNotificationStore(t)
		defer store.Close()
		committer := NewAutomationExecutionNotificationCommitter(store)
		execution := automationNotificationExecution("insert-error")
		execution.Candidate = map[string]any{"bad": make(chan int)}
		if err := committer.CommitAutomationExecutionNotification(t.Context(), execution, automationNotificationEvent("insert-error", "insert-error")); err == nil {
			t.Fatal("execution insert error was ignored")
		}
	})

	t.Run("post failure inspection", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			committed bool
			err       error
		}{
			{name: "inspection error", err: errors.New("post failure inspection failed")},
			{name: "concurrent commit", committed: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				store := openAutomationNotificationStore(t)
				defer store.Close()
				committer := NewAutomationExecutionNotificationCommitter(store)
				calls := 0
				committer.inspectCommit = func(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) (bool, error) {
					calls++
					if calls == 1 {
						return false, nil
					}
					return test.committed, test.err
				}
				execution := automationNotificationExecution("post-" + test.name)
				execution.Candidate = map[string]any{"bad": make(chan int)}
				err := committer.CommitAutomationExecutionNotification(t.Context(), execution, automationNotificationEvent("post-"+test.name, "post-"+test.name))
				if test.committed && err != nil {
					t.Fatalf("concurrent commit was not recovered: %v", err)
				}
				if !test.committed && !errors.Is(err, test.err) {
					t.Fatalf("error=%v want=%v", err, test.err)
				}
			})
		}
	})
}

func openAutomationNotificationStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "automation-notification.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func automationNotificationExecution(id string) automationmodel.AutomationRuleExecution {
	return automationmodel.AutomationRuleExecution{
		ID: id, WorkspaceID: "workspace-a", RuleKey: "sync-order", ObjectKey: "order", RecordID: "order-1",
		Phase: "after", Operation: "update", Status: "succeeded", ActorID: "user-1", Candidate: map[string]any{}, Trace: map[string]any{},
	}
}

func automationNotificationEvent(id, sourceID string) notificationmodel.NotificationEvent {
	return notificationmodel.NotificationEvent{
		ID: id, WorkspaceID: "workspace-a", Source: "automation", SourceEventID: sourceID, EventType: "automation.execution.completed",
		Category: "automation", Severity: "info", RecipientUserIDs: []string{"user-1"},
		SubjectType: "automation_rule", SubjectID: "sync-order", OccurredAt: "2026-07-28T01:00:00Z", Status: "queued",
		CreatedAt: "2026-07-28T01:00:00Z", UpdatedAt: "2026-07-28T01:00:00Z",
	}
}

func assertAutomationNotificationCounts(t *testing.T, store *database.RuntimeStore, executionID string, wantExecutions, wantEvents int) {
	t.Helper()
	var executions, events int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier("_automation_runs")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1)+" AND "+store.Identifier("run_kind")+" = "+store.Placeholder(2)+" AND "+store.Identifier("id")+" = "+store.Placeholder(3), "workspace-a", "rule", executionID).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier("_notification_events")).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if executions != wantExecutions || events != wantEvents {
		t.Fatalf("executions=%d want=%d events=%d want=%d", executions, wantExecutions, events, wantEvents)
	}
}
