package automationnotification

import (
	"context"
	"database/sql"
	"fmt"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

type AutomationExecutionNotificationCommitter struct {
	store            *database.RuntimeStore
	executions       automationpersistence.AutomationExecutionStore
	notifications    notificationpersistence.InboxEventWriter
	beginTx          func(context.Context) (*sql.Tx, error)
	inspectExecution func(context.Context, automationmodel.AutomationRuleExecution) (bool, error)
	inspectCommit    func(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) (bool, error)
}

func NewAutomationExecutionNotificationCommitter(store *database.RuntimeStore) AutomationExecutionNotificationCommitter {
	return AutomationExecutionNotificationCommitter{
		store: store, executions: automationpersistence.NewAutomationExecutionStore(store), notifications: notificationpersistence.NewInboxEventWriter(store),
	}
}

func (s AutomationExecutionNotificationCommitter) CommitAutomationExecution(ctx context.Context, execution automationmodel.AutomationRuleExecution) error {
	if found, err := s.executionCommitted(ctx, execution); err != nil || found {
		return err
	}
	if _, err := s.executions.InsertExecution(ctx, execution.WorkspaceID, execution); err != nil {
		if found, checkErr := s.executionCommitted(ctx, execution); checkErr != nil {
			return checkErr
		} else if found {
			return nil
		}
		return err
	}
	return nil
}

func (s AutomationExecutionNotificationCommitter) CommitAutomationExecutionNotification(ctx context.Context, execution automationmodel.AutomationRuleExecution, event notificationmodel.NotificationEvent) error {
	if committed, err := s.committed(ctx, execution, event); err != nil || committed {
		return err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return fmt.Errorf("begin automation execution notification: %w", err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	if _, err = s.executions.InsertExecution(txCtx, execution.WorkspaceID, execution); err == nil {
		event.WorkspaceID = execution.WorkspaceID
		err = s.notifications.InsertEventTx(txCtx, tx, event)
	}
	if err == nil {
		err = tx.Commit()
		if err != nil {
			err = fmt.Errorf("commit automation execution notification: %w", err)
		}
	}
	if err == nil {
		s.notifications.PublishCommittedEventWakeup(event)
		return nil
	}
	_ = tx.Rollback()
	if committed, checkErr := s.committed(ctx, execution, event); checkErr != nil {
		return checkErr
	} else if committed {
		return nil
	}
	return err
}

func (s AutomationExecutionNotificationCommitter) committed(ctx context.Context, execution automationmodel.AutomationRuleExecution, event notificationmodel.NotificationEvent) (bool, error) {
	if s.inspectCommit != nil {
		return s.inspectCommit(ctx, execution, event)
	}
	var executionCount, eventCount int
	found, err := s.executionCommitted(ctx, execution)
	if err != nil {
		return false, err
	}
	if found {
		executionCount = 1
	}
	eventCount, err = s.notifications.CommittedCount(ctx, event)
	if err != nil {
		return false, fmt.Errorf("inspect automation notification replay: %w", err)
	}
	if executionCount == 1 && eventCount == 1 {
		return true, nil
	}
	if executionCount != eventCount {
		return false, fmt.Errorf("automation execution and notification atomic state is inconsistent")
	}
	return false, nil
}

func (s AutomationExecutionNotificationCommitter) executionCommitted(ctx context.Context, execution automationmodel.AutomationRuleExecution) (bool, error) {
	if s.inspectExecution != nil {
		return s.inspectExecution(ctx, execution)
	}
	var count int
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("automation_rule_executions") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	if err := s.store.DB().QueryRowContext(ctx, query, execution.WorkspaceID, execution.ID).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect automation execution replay: %w", err)
	}
	return count == 1, nil
}

func (s AutomationExecutionNotificationCommitter) begin(ctx context.Context) (*sql.Tx, error) {
	if s.beginTx != nil {
		return s.beginTx(ctx)
	}
	return s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
}
