package workflow

import (
	"context"
	"database/sql"
	"fmt"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

type WorkflowTaskNotificationStore struct {
	store         *database.RuntimeStore
	decisions     WorkflowDecisionStore
	notifications notificationpersistence.InboxEventWriter
}

func NewWorkflowTaskNotificationStore(store *database.RuntimeStore) WorkflowTaskNotificationStore {
	return WorkflowTaskNotificationStore{store: store, decisions: NewWorkflowDecisionStore(store), notifications: notificationpersistence.NewInboxEventWriter(store)}
}

func (s WorkflowTaskNotificationStore) CommitWorkflowTaskNotification(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask, event notificationmodel.NotificationEvent) error {
	return s.commitWorkflowTaskNotification(ctx, workspaceID, task, event, false)
}

func (s WorkflowTaskNotificationStore) CommitWorkflowTaskOpeningNotification(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask, event notificationmodel.NotificationEvent) error {
	return s.commitWorkflowTaskNotification(ctx, workspaceID, task, event, true)
}

func (s WorkflowTaskNotificationStore) CommitWorkflowTaskReminderNotification(ctx context.Context, workspaceID string, processEvent workflowmodel.WorkflowProcessEvent, event notificationmodel.NotificationEvent) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin workflow task reminder notification: %w", err)
	}
	defer tx.Rollback()
	processEvent.WorkspaceID, event.WorkspaceID = workspaceID, workspaceID
	if err := s.decisions.insertEventTx(ctx, tx, processEvent); err != nil {
		return err
	}
	if err := s.notifications.InsertEventTx(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow task reminder notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return nil
}

func (s WorkflowTaskNotificationStore) CommitWorkflowTaskEscalationNotification(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask, processEvent workflowmodel.WorkflowProcessEvent, events []notificationmodel.NotificationEvent) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin workflow task escalation notification: %w", err)
	}
	defer tx.Rollback()
	task.WorkspaceID, processEvent.WorkspaceID = workspaceID, workspaceID
	if err := s.decisions.updateTaskTx(ctx, tx, task); err != nil {
		return err
	}
	if err := s.decisions.insertEventTx(ctx, tx, processEvent); err != nil {
		return err
	}
	for _, event := range events {
		event.WorkspaceID = workspaceID
		if err := s.notifications.InsertEventTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow task escalation notification: %w", err)
	}
	for _, event := range events {
		event.WorkspaceID = workspaceID
		s.notifications.PublishCommittedEventWakeup(event)
	}
	return nil
}

func (s WorkflowTaskNotificationStore) commitWorkflowTaskNotification(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask, event notificationmodel.NotificationEvent, update bool) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin workflow task notification: %w", err)
	}
	defer tx.Rollback()
	task.WorkspaceID, event.WorkspaceID = workspaceID, workspaceID
	var taskErr error
	if update {
		taskErr = s.decisions.updateTaskTx(ctx, tx, task)
	} else {
		taskErr = s.decisions.insertTaskTx(ctx, tx, task)
	}
	if taskErr != nil {
		return taskErr
	}
	if err := s.notifications.InsertEventTx(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow task notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return nil
}
