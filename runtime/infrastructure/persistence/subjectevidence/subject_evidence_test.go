package subjectevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	notificationstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	publicationstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	workflowstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func fixture(t *testing.T) (*database.RuntimeStore, *Handler, context.Context) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "evidence.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := New(store, func(context.Context, string, string) ([]recordmodel.SubjectRecordReference, error) {
		return []recordmodel.SubjectRecordReference{{ObjectKey: "member_profile", RecordID: "profile-alice"}}, nil
	})
	return store, h, requestcontext.WithWorkspaceID(t.Context(), "workspace-a")
}

// Seed every actual required schema column, so the test uses the real persisted
// layouts rather than a hand-built simplified history table.
func seed(t *testing.T, store *database.RuntimeStore, table, id, workspace string, override map[string]any) {
	t.Helper()
	rows, err := store.DB().QueryContext(t.Context(), "PRAGMA table_info("+store.Identifier(table)+")")
	if err != nil {
		t.Fatal(err)
	}
	columns := []string{}
	values := []any{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		var value any = ""
		if strings.Contains(strings.ToUpper(kind), "INT") {
			value = 0
		}
		if strings.HasSuffix(name, "_json") {
			value = "{}"
		}
		if defaultValue != nil && notnull == 0 {
			value = nil
		}
		if v, ok := override[name]; ok {
			value = v
		}
		if name == "id" {
			value = id
		}
		if name == "workspace_id" {
			value = workspace
		}
		if name == "idempotency_key" || name == "operation_id" || name == "dedup_key" || name == "token_hash" || name == "audit_event_id" {
			value = id + ":" + name
		}
		columns = append(columns, store.Identifier(name))
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = "?"
	}
	if _, err = store.DB().ExecContext(t.Context(), "INSERT INTO "+store.TableIdentifier(table)+" ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(placeholders, ",")+")", values...); err != nil {
		t.Fatalf("seed %s %s: %v", table, id, err)
	}
}
func rawRow(t *testing.T, store *database.RuntimeStore, table, id, workspace string) []byte {
	t.Helper()
	rows, err := store.DB().QueryContext(t.Context(), "SELECT * FROM "+store.TableIdentifier(table)+" WHERE workspace_id=? AND id=?", workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if !rows.Next() {
		return nil
	}
	if err = rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRuntimeEvidenceErasureUsesDurableScopeAndRollsBackBeforeRetry(t *testing.T) {
	store, h, ctx := fixture(t)
	h.events = func(context.Context, string, string, []recordmodel.SubjectRecordReference) ([]string, error) {
		return []string{"source-event-alice"}, nil
	}
	alice := []rowReference{}
	peers := map[rowReference][]byte{}
	for _, item := range []struct{ subject, workspace, prefix string }{{"alice", "workspace-a", "a"}, {"bob", "workspace-a", "b"}, {"alice", "workspace-b", "c"}} {
		for _, s := range specs {
			id := item.prefix + s.table
			private := item.prefix + "@private.example.test"
			payload := `{"email":"` + private + `"}`
			override := map[string]any{"status": "completed", "state": "active", "actor_id": item.subject, "initiator_id": item.subject, "created_by": item.subject, "requested_by": item.subject, "requester_user_id": item.subject, "user_id": item.subject, "assignee_user_id": item.subject, "completed_by": item.subject, "configured_by": item.subject, "object_key": "member_profile", "record_id": "profile-" + item.subject, "target_id": "profile-" + item.subject, "process_id": item.prefix + "_workflow_process_instances", "result_json": payload, "input_json": payload, "output_json": payload, "variables_json": payload, "definition_json": payload, "action_json": payload, "payload_json": payload, "candidate_json": payload, "trace_json": payload, "metadata_json": payload, "intent_json": payload, "assignee_name": private, "resolver_snapshot_json": payload, "assignee_snapshot_json": payload, "comment": private, "title": private, "workflow_name": private, "name": private, "reason": private, "summary": private, "error": private, "last_error": private, "message": private, "related_ids_json": payload, "evidence_json": payload, "next_action": private, "incident_ref": private, "alert_target": private, "revocation_note": private, "reference": private}
			if s.table == "_publication_outbox" {
				override["publication_type"] = "integration.connector"
				override["status"] = "queued"
			}
			seed(t, store, s.table, id, item.workspace, override)
			ref := rowReference{Table: s.table, ID: id}
			if item.prefix == "a" {
				alice = append(alice, ref)
			} else {
				peers[ref] = rawRow(t, store, s.table, id, item.workspace)
			}
		}
	}
	// A manager's response for the subject's business record is still a copy.
	seed(t, store, "_action_executions", "manager-response", "workspace-a", map[string]any{"actor_id": "manager", "object_key": "member_profile", "record_id": "profile-alice", "status": "completed", "result_json": `{"email":"a@private.example.test"}`})
	alice = append(alice, rowReference{Table: "_action_executions", ID: "manager-response"})
	seed(t, store, "_publication_outbox", "manager-publication", "workspace-a", map[string]any{"created_by": "manager", "event_id": "source-event-alice", "status": "queued", "publication_type": "integration.connector", "payload_json": `{"email":"a@private.example.test"}`})
	alice = append(alice, rowReference{Table: "_publication_outbox", ID: "manager-publication"})
	export, err := h.ExportSubjectForRequest(ctx, "export", "workspace-a", "alice")
	if err != nil || !bytes.Contains(export, []byte("a@private.example.test")) {
		t.Fatalf("actual export did not include private copies: err=%v", err)
	}
	p, err := h.PrepareSubjectErasure(ctx, "erase-alice", "workspace-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p, []byte("private.example.test")) {
		t.Fatal("prepared journal retained private payload")
	}
	if p2, err := h.PrepareSubjectErasure(ctx, "erase-alice", "workspace-a", "alice"); err != nil || !bytes.Equal(p, p2) {
		t.Fatalf("prepare replay changed: %v", err)
	}
	if _, err = h.ErasePreparedSubject(ctx, "erase-alice", "workspace-a", "alice", append(bytes.Clone(p), byte(' ')), nil); err == nil {
		t.Fatal("accepted a plan different from source receipt")
	}
	if _, err = h.ErasePreparedSubject(requestcontext.WithWorkspaceID(ctx, "workspace-b"), "erase-alice", "workspace-a", "alice", p, nil); err == nil {
		t.Fatal("accepted wrong workspace context")
	}
	if _, err = h.ErasePreparedSubject(ctx, "erase-alice", "workspace-a", "alice", p, []lifecyclemodel.LegalHold{{ID: "hold"}}); err == nil {
		t.Fatal("ignored hold")
	}
	if _, err = store.DB().ExecContext(ctx, `CREATE TRIGGER fail_subject_cleanup BEFORE UPDATE ON _workflow_tasks WHEN OLD.id='a_workflow_tasks' BEGIN SELECT RAISE(ABORT,'injected cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.ErasePreparedSubject(ctx, "erase-alice", "workspace-a", "alice", p, nil); err == nil {
		t.Fatal("cleanup failure did not propagate")
	}
	for _, ref := range alice {
		if ref.Table == "_action_assurance_grants" {
			continue
		}
		if !bytes.Contains(rawRow(t, store, ref.Table, ref.ID, "workspace-a"), []byte("a@private.example.test")) {
			t.Fatalf("failed transaction partially erased %s", ref.Table)
		}
	}
	if _, err = store.DB().ExecContext(ctx, `DROP TRIGGER fail_subject_cleanup`); err != nil {
		t.Fatal(err)
	}
	out, err := h.ErasePreparedSubject(ctx, "erase-alice", "workspace-a", "alice", p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out2, err := h.ErasePreparedSubject(ctx, "erase-alice", "workspace-a", "alice", p, nil); err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("execution replay differs: %v", err)
	}
	for _, ref := range alice {
		if bytes.Contains(rawRow(t, store, ref.Table, ref.ID, "workspace-a"), []byte("a@private.example.test")) {
			t.Fatalf("private copy survived %s", ref.Table)
		}
	}
	for ref, before := range peers {
		workspace := "workspace-a"
		if strings.HasPrefix(ref.ID, "c") {
			workspace = "workspace-b"
		}
		if after := rawRow(t, store, ref.Table, ref.ID, workspace); !bytes.Equal(before, after) {
			t.Fatalf("peer changed %s/%s", ref.Table, ref.ID)
		}
	}
	worker := workflowstore.NewWorkflowWorkerStore(store)
	if err = worker.UpdateExecution(ctx, "workspace-a", workflowmodel.WorkflowExecution{ID: "a_workflow_executions", Status: "pending", Payload: map[string]any{"email": "a@private.example.test"}}); err == nil {
		t.Fatal("cached workflow rewrote erased payload")
	}
	if err = worker.InsertProcessEvent(ctx, "workspace-a", workflowmodel.WorkflowProcessEvent{ID: "late-event", ProcessID: "a_workflow_process_instances", ActorID: "manager", Summary: "a@private.example.test"}); err == nil {
		t.Fatal("late event reintroduced process content")
	}
	if err = worker.InsertExecution(ctx, "workspace-a", workflowmodel.WorkflowExecution{ID: "new-workflow", ActorID: "alice", Status: "pending"}); err == nil {
		t.Fatal("new workflow accepted erased actor")
	}
	_, claim, err := publicationstore.NewWorkerStore(store).ClaimOutbox(ctx, "workspace-a", "a_publication_outbox", "late-worker", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil || claim {
		t.Fatalf("queued publication was still claimable claim=%v err=%v", claim, err)
	}
	_, err = actionstore.NewActionBusinessExecutionStore(store).TryBeginExecution(ctx, actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ActorID: "alice", ObjectKey: "member_profile", RecordID: "profile-alice", ActionKey: "reopen", IdempotencyKey: "late-action"}, RequestFingerprint: "private", LeaseOwner: "late"})
	if err == nil {
		t.Fatal("new action accepted erased actor")
	}
}

func TestRuntimeEvidenceBusyAndEmptyParentsDoNotErasePeerWorkflows(t *testing.T) {
	store, h, ctx := fixture(t)
	seed(t, store, "_workflow_executions", "peer-unbound", "workspace-a", map[string]any{"actor_id": "bob", "status": "pending", "process_id": "", "payload_json": `{"email":"bob@private.test"}`})
	seed(t, store, "_publication_outbox", "busy", "workspace-a", map[string]any{"created_by": "alice", "status": "sending", "publication_type": "integration.connector", "payload_json": `{"email":"alice@private.test"}`})
	if _, err := h.PrepareSubjectErasure(ctx, "busy-erase", "workspace-a", "alice"); err == nil {
		t.Fatal("allowed erasure during external delivery")
	}
	var fences int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM _subject_evidence_erasure_fences`).Scan(&fences); err != nil || fences != 0 {
		t.Fatalf("busy preparation left fences: %d %v", fences, err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE _publication_outbox SET status='queued' WHERE id='busy'`); err != nil {
		t.Fatal(err)
	}
	p, err := h.PrepareSubjectErasure(ctx, "busy-erase", "workspace-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p, []byte("peer-unbound")) {
		t.Fatal("empty process predicate selected peer")
	}
	if _, err = h.ErasePreparedSubject(ctx, "busy-erase", "workspace-a", "alice", p, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rawRow(t, store, "_workflow_executions", "peer-unbound", "workspace-a"), []byte("bob@private.test")) {
		t.Fatal("erased unbound peer workflow")
	}
}

func TestRuntimeEvidenceIncludesTypedNotificationIntentAndRejectsNewPublication(t *testing.T) {
	store, h, ctx := fixture(t)
	if err := store.BindNotificationSaaSPublications(database.NotificationSaaSPublicationScope{WorkspaceID: "workspace-a", ApplicationKey: "runtime"}); err != nil {
		t.Fatal(err)
	}
	publication := notificationstore.NewPublicationOutboxStore(store)
	for _, subject := range []string{"alice", "bob"} {
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		intent := notificationmodel.NotificationIntent{ID: subject + "-notification", WorkspaceID: "workspace-a", SourceEventID: subject + "-event", RecipientUserIDs: []string{subject}, Variables: map[string]any{"email": subject + "@private.test"}}
		if err = publication.InsertIntentTx(ctx, tx, intent); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	p, err := h.PrepareSubjectErasure(ctx, "notify-erase", "workspace-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.ErasePreparedSubject(ctx, "notify-erase", "workspace-a", "alice", p, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawRow(t, store, "_publication_outbox", "alice-notification", "workspace-a"), []byte("alice@private.test")) {
		t.Fatal("intent private variables survived")
	}
	if !bytes.Contains(rawRow(t, store, "_publication_outbox", "bob-notification", "workspace-a"), []byte("bob@private.test")) {
		t.Fatal("peer intent changed")
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = publication.InsertIntentTx(ctx, tx, notificationmodel.NotificationIntent{ID: "late-notification", WorkspaceID: "workspace-a", SourceEventID: "late-event", RecipientUserIDs: []string{"alice"}}); err == nil {
		t.Fatal("new subject intent bypassed fence")
	}
}
