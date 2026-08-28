package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

type notificationSubjectLifecycleFixture struct{ database *sql.DB }

func (notificationSubjectLifecycleFixture) Owner(context.Context) string { return "notification" }
func (notificationSubjectLifecycleFixture) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (notificationSubjectLifecycleFixture) ExportSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (f notificationSubjectLifecycleFixture) EraseSubject(ctx context.Context, workspaceID, subjectID string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	_, err := f.database.ExecContext(ctx, "UPDATE notification_inbox_items SET title = '[erased]' WHERE workspace_id = ? AND recipient_user_id = ?", workspaceID, subjectID)
	return json.RawMessage(`{"content_redacted":true}`), err
}

func TestSubjectErasureRegistersBackupReplayEvidence(t *testing.T) {
	service, store := newLifecycleApplicationTestService(t)
	request, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "user-1", Reason: "erasure request"}, lifecycleAdmin("workspace-a", "requester"))
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-evidence", lifecycleAdmin("workspace-a", "verifier"))
	if err == nil {
		request, err = service.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "reviewer"))
	}
	if err == nil {
		request, err = service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "approver"))
	}
	if err == nil {
		request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "executor"))
	}
	if err != nil || request.Status != lifecyclemodel.SubjectRequestSucceeded || !request.BackupPending {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	registrations, err := lifecyclepersistence.NewLifecycleStore(store).ListPendingDeletionRegistrations(t.Context(), "workspace-a", 10)
	if err != nil || len(registrations) != 1 || registrations[0].ResolvedIdentity != request.ResolvedIdentity {
		t.Fatalf("registrations=%#v err=%v", registrations, err)
	}
	replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, lifecycleAdmin("workspace-a", "restore-operator"))
	if err != nil || replayed != 1 {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
}

func TestSubjectErasureIsBlockedByLegalHold(t *testing.T) {
	service, _ := newLifecycleApplicationTestService(t)
	admin := lifecycleAdmin("workspace-a", "legal")
	request, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "user-held", Reason: "erasure request"}, lifecycleAdmin("workspace-a", "requester"))
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-evidence", lifecycleAdmin("workspace-a", "verifier"))
	if err == nil {
		request, err = service.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "reviewer"))
	}
	if err == nil {
		request, err = service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "approver"))
	}
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	hold, err := service.CreateLegalHold(t.Context(), lifecyclemodel.LegalHold{WorkspaceID: "workspace-a", ResourceType: "data_subject", ResourceID: request.ResolvedIdentity, Reason: "legal case", Authority: "legal", StartsAt: now.Add(-time.Minute), ReviewAt: now.Add(time.Hour), AuditEvidence: "case-1"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "executor"))
	if err != nil || request.Status != lifecyclemodel.SubjectRequestFailed || request.LastError == "" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	ended, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "case-closed", now.Add(-time.Second), admin)
	if err != nil || ended.EndsAt == nil {
		t.Fatalf("ended=%#v err=%v", ended, err)
	}
	request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "retry-executor"))
	if err != nil || request.Status != lifecyclemodel.SubjectRequestSucceeded || request.ExecutionAttempt != 2 || !request.ExecutionLeaseEnd.IsZero() {
		t.Fatalf("retried request=%#v err=%v", request, err)
	}
}

func TestNotificationSubjectErasureEndToEndHonorsLegalHoldBeforeAnonymizing(t *testing.T) {
	_, store := newLifecycleApplicationTestService(t)
	if err := notificationsdkfixture.EnsureModuleSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{
		Repository:      lifecyclepersistence.NewLifecycleStore(store),
		SubjectResolver: subjectResolverStub{},
		SubjectHandlers: []lifecyclecontract.SubjectDataHandler{notificationSubjectLifecycleFixture{database: store.DB()}},
	})
	request, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "held-notification-user", Reason: "erasure request"}, lifecycleAdmin("workspace-a", "requester"))
	if err == nil {
		request, err = service.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-evidence", lifecycleAdmin("workspace-a", "verifier"))
	}
	if err == nil {
		request, err = service.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "reviewer"))
	}
	if err == nil {
		request, err = service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "approver"))
	}
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item := notificationmodel.NotificationInboxItem{
		ID: "held-notification", WorkspaceID: "workspace-a", RecipientUserID: request.ResolvedIdentity, Surface: "business_workspace",
		EventID: "held-event", EventType: "workflow.task.completed", Source: "workflow", Category: "approval", Severity: "info",
		Title: "Legally held notification", Body: "This content must remain intact while held.", SubjectType: "workflow_task", SubjectID: "task-held",
		ActionState: notificationmodel.NotificationActionCompleted, OccurrenceCount: 1, FirstOccurredAt: notificationmodel.NotificationTimestamp(now), LastOccurredAt: notificationmodel.NotificationTimestamp(now),
		CreatedAt: notificationmodel.NotificationTimestamp(now), UpdatedAt: notificationmodel.NotificationTimestamp(now),
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB().ExecContext(t.Context(), "INSERT INTO notification_inbox_items (id, workspace_id, recipient_user_id, surface, event_id, event_type, source, category, severity, title, body, search_text, payload_json, subject_type, subject_id, action_state, alert_state, group_key, occurrence_count, first_occurred_at, last_occurred_at, read_at, archived_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		item.ID, item.WorkspaceID, item.RecipientUserID, item.Surface, item.EventID, item.EventType, item.Source, item.Category, item.Severity, item.Title, item.Body, "legally held notification", string(raw), item.SubjectType, item.SubjectID, item.ActionState, "", "", 1, item.FirstOccurredAt, item.LastOccurredAt, "", "", "", item.CreatedAt, item.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := service.CreateLegalHold(t.Context(), lifecyclemodel.LegalHold{WorkspaceID: "workspace-a", ResourceType: "data_subject", ResourceID: request.ResolvedIdentity, Reason: "legal case", Authority: "legal", StartsAt: now.Add(-time.Minute), ReviewAt: now.Add(time.Hour), AuditEvidence: "case-notification"}, lifecycleAdmin("workspace-a", "legal"))
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "executor"))
	if err != nil || request.Status != lifecyclemodel.SubjectRequestFailed {
		t.Fatalf("held request=%#v err=%v", request, err)
	}
	var title string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT title FROM notification_inbox_items WHERE workspace_id = ? AND id = ?", "workspace-a", item.ID).Scan(&title); err != nil || title != item.Title {
		t.Fatalf("held title=%q err=%v", title, err)
	}
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "case-closed", now.Add(-time.Second), lifecycleAdmin("workspace-a", "legal")); err != nil {
		t.Fatal(err)
	}
	request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "retry-executor"))
	if err != nil || request.Status != lifecyclemodel.SubjectRequestSucceeded || request.ExecutionAttempt != 2 {
		t.Fatalf("released request=%#v err=%v", request, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), "SELECT title FROM notification_inbox_items WHERE workspace_id = ? AND id = ?", "workspace-a", item.ID).Scan(&title); err != nil || title != "[erased]" {
		t.Fatalf("erased title=%q err=%v", title, err)
	}
}

func TestExpiredSubjectExecutionCanBeRecovered(t *testing.T) {
	service, store := newLifecycleApplicationTestService(t)
	request, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "user-recover", Reason: "erasure request"}, lifecycleAdmin("workspace-a", "requester"))
	if err == nil {
		request, err = service.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-evidence", lifecycleAdmin("workspace-a", "verifier"))
	}
	if err == nil {
		request, err = service.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "reviewer"))
	}
	if err == nil {
		request, err = service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "approver"))
	}
	if err != nil {
		t.Fatal(err)
	}
	request.Status = lifecyclemodel.SubjectRequestExecuting
	request.ExecutionAttempt = 1
	request.ExecutionLeaseEnd = time.Now().UTC().Add(-time.Minute)
	request.UpdatedAt = request.ExecutionLeaseEnd.Add(-5 * time.Minute)
	if err := lifecyclepersistence.NewLifecycleStore(store).SaveSubjectRequest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "recovery-executor"))
	if err != nil || recovered.Status != lifecyclemodel.SubjectRequestSucceeded || recovered.ExecutionAttempt != 2 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}
