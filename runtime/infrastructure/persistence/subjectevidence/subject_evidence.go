// Package subjectevidence owns account cleanup of Runtime execution copies.
package subjectevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type ResourceResolver func(context.Context, string, string) ([]recordmodel.SubjectRecordReference, error)
type EventResolver func(context.Context, string, string, []recordmodel.SubjectRecordReference) ([]string, error)
type Handler struct {
	store   *database.RuntimeStore
	resolve ResourceResolver
	events  EventResolver
}

func New(store *database.RuntimeStore, resolve ResourceResolver, events ...EventResolver) *Handler {
	h := &Handler{store: store, resolve: resolve}
	if len(events) > 0 {
		h.events = events[0]
	}
	return h
}
func (*Handler) Owner(context.Context) string { return "runtime_evidence" }

type rowReference struct {
	Table string `json:"table"`
	ID    string `json:"id"`
}
type plan struct {
	RequestID   string                               `json:"request_id"`
	WorkspaceID string                               `json:"workspace_id"`
	SubjectID   string                               `json:"subject_id"`
	Resources   []recordmodel.SubjectRecordReference `json:"resources"`
	EventIDs    []string                             `json:"event_ids"`
	Rows        []rowReference                       `json:"rows"`
}
type spec struct {
	table, status string
	set           map[string]any
	busy          []string
	fence         bool
}

var specs = []spec{
	{table: "_action_executions", status: "status", busy: []string{"pending", "processing"}, fence: true, set: map[string]any{"result_json": "{}", "idempotency_key": "", "request_fingerprint": "", "actor_id": "anonymous", "role_key": "", "status": "failed", "error_code": "runtime.subject_erased", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_workflow_process_instances", status: "status", set: map[string]any{"workflow_name": "", "definition_json": "{}", "initiator_id": "anonymous", "initiator_role_key": "", "current_node_ids_json": "[]", "variables_json": "{}", "result_json": "{}", "status": "cancelled", "error_code": "runtime.subject_erased"}},
	{table: "_workflow_executions", status: "status", busy: []string{"running", "processing", "executing"}, fence: true, set: map[string]any{"name": "", "action_json": "{}", "payload_json": "{}", "result_json": "{}", "actor_id": "anonymous", "run_as": "", "idempotency_key": "", "last_error": "", "message": "", "status": "cancelled", "next_run_at": "", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_workflow_node_instances", status: "status", busy: []string{"running", "processing"}, set: map[string]any{"input_json": "{}", "output_json": "{}", "status": "cancelled", "error_code": "runtime.subject_erased"}},
	{table: "_workflow_tasks", status: "status", set: map[string]any{"title": "", "assignee_user_id": "anonymous", "assignee_name": "", "assignee_role_key": "", "resolver_snapshot_json": "[]", "comment": "", "completed_by": "anonymous", "status": "cancelled"}},
	{table: "_workflow_process_events", set: map[string]any{"actor_id": "anonymous", "summary": "", "metadata_json": "{}"}},
	{table: "_workflow_route_steps", status: "status", set: map[string]any{"title": "", "assignee_snapshot_json": "[]", "configured_by": "anonymous", "status": "cancelled"}},
	{table: "_publication_outbox", status: "status", busy: []string{"sending", "processing", "running"}, fence: true, set: map[string]any{"intent_json": "{}", "payload_json": "{}", "created_by": "anonymous", "request_ref": "", "response_ref": "", "request_fingerprint": "", "error": "", "last_error": "", "status": "failed", "last_error_code": "runtime.subject_erased", "next_attempt_at": "", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_action_assurance_grants"},
	{table: "_upload_subject_bindings"},
	{table: "_record_mutation_executions", status: "status", busy: []string{"processing", "pending"}, fence: true, set: map[string]any{"result_json": "{}", "actor_id": "anonymous", "idempotency_key": "", "request_fingerprint": "", "status": "failed", "error_code": "runtime.subject_erased", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_report_export_prepare_receipts", status: "status", busy: []string{"processing"}, fence: true, set: map[string]any{"payload_json": "", "requester_user_id": "anonymous", "request_fingerprint": "", "completion_fingerprint": "", "idempotency_key": "", "status": "failed", "terminal_error_code": "runtime.subject_erased", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_automation_rule_executions", status: "status", busy: []string{"processing", "running"}, set: map[string]any{"actor_id": "anonymous", "role_key": "", "candidate_json": "{}", "trace_json": "{}", "status": "failed", "error_code": "runtime.subject_erased"}},
	{table: "_automation_instruction_executions", status: "status", busy: []string{"processing", "running"}, fence: true, set: map[string]any{"idempotency_key": "", "result_json": "{}", "status": "failed", "error_code": "runtime.subject_erased", "lease_owner": "", "lease_expires_at": ""}},
	{table: "_operation_requests", status: "status", busy: []string{"running", "executing", "processing"}, set: map[string]any{"requested_by": "anonymous", "reason": "", "reference": "", "result_json": "{}", "related_ids_json": "[]", "evidence_json": "{}", "next_action": "", "request_fingerprint": "", "idempotency_key": "", "status": "failed", "error_code": "runtime.subject_erased"}},
	{table: "_operation_break_glass_grants", set: map[string]any{"actor_id": "anonymous", "approver_ids_json": "[]", "reason": "", "incident_ref": "", "alert_target": "", "revocation_note": "", "revoked_by": "anonymous", "state": "revoked"}},
}

func scope(ctx context.Context, workspace, subject string) error {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(subject) == "" || requestcontext.WorkspaceID(ctx) != workspace || database.ActionExecutionTransaction(ctx) != nil {
		return fmt.Errorf("Runtime subject evidence scope invalid")
	}
	return nil
}
func refsPredicate(resources []recordmodel.SubjectRecordReference) query.Predicate {
	return refsPredicateWithColumn(resources, "record_id")
}
func refsPredicateWithColumn(resources []recordmodel.SubjectRecordReference, recordColumn string) query.Predicate {
	items := []query.Predicate{query.AlwaysFalse()}
	for _, ref := range resources {
		items = append(items, query.And(query.Equal("object_key", ref.ObjectKey), query.Equal(recordColumn, ref.RecordID)))
	}
	return query.Or(items...)
}
func in(column string, ids []string) query.Predicate {
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	if len(values) == 0 {
		return query.AlwaysFalse()
	}
	return query.In(column, values...)
}
func (h *Handler) collect(ctx context.Context, tx *sql.Tx, workspace, subject string, resources []recordmodel.SubjectRecordReference, events []string, lock bool) ([]rowReference, error) {
	items := []rowReference{}
	processIDs, executionIDs, actionIDs := []string{}, []string{}, []string{}
	for _, s := range specs {
		var predicate query.Predicate
		switch s.table {
		case "_action_executions":
			predicate = query.Or(query.Equal("actor_id", subject), refsPredicate(resources))

		case "_record_mutation_executions":
			predicate = query.Or(query.Equal("actor_id", subject), refsPredicateWithColumn(resources, "target_id"))
		case "_report_export_prepare_receipts":
			predicate = query.Equal("requester_user_id", subject)
		case "_automation_rule_executions":
			predicate = query.Or(query.Equal("actor_id", subject), refsPredicate(resources))
		case "_automation_instruction_executions":
			predicate = refsPredicate(resources)
		case "_operation_requests":
			predicate = query.Equal("requested_by", subject)
		case "_operation_break_glass_grants":
			predicate = query.Equal("actor_id", subject)
		case "_workflow_process_instances":
			predicate = query.Or(query.Equal("initiator_id", subject), refsPredicate(resources))
		case "_workflow_executions":
			predicate = query.Or(query.Equal("actor_id", subject), refsPredicate(resources), in("process_id", processIDs))
		case "_workflow_tasks":
			predicate = query.Or(in("process_id", processIDs), query.Equal("assignee_user_id", subject), query.Equal("completed_by", subject))
		case "_workflow_process_events":
			predicate = query.Or(in("process_id", processIDs), query.Equal("actor_id", subject))
		case "_workflow_route_steps":
			predicate = query.Or(in("process_id", processIDs), query.Equal("configured_by", subject))
		case "_workflow_node_instances":
			predicate = in("process_id", processIDs)
		case "_upload_subject_bindings":
			predicate = query.Equal("user_id", subject)
		case "_action_assurance_grants":
			predicate = query.Or(query.Equal("user_id", subject), refsPredicate(resources))
		case "_publication_outbox":
			predicate = query.Or(query.Equal("created_by", subject), in("request_ref", append(slices.Clone(actionIDs), executionIDs...)), in("event_id", events), in("source_event_id", events))
		}
		columns := []string{"id"}
		if s.status != "" {
			columns = append(columns, s.status)
		}
		builder := query.NewWorkspaceSelectBuilder(h.store.SQLRenderer, s.table, workspace).Columns(columns...).Where(predicate).OrderBy(query.Ascending("id"))
		if lock && h.store.RuntimeEngine.Capabilities().RowLock {
			builder.ForUpdate()
		}
		statement, args, err := builder.Build()
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var status sql.NullString
			destinations := []any{&id}
			if s.status != "" {
				destinations = append(destinations, &status)
			}
			if err = rows.Scan(destinations...); err != nil {
				rows.Close()
				return nil, err
			}
			if lock && slices.Contains(s.busy, status.String) {
				rows.Close()
				return nil, fmt.Errorf("runtime.subject_evidence_busy: %s", s.table)
			}
			items = append(items, rowReference{Table: s.table, ID: id})
			switch s.table {
			case "_workflow_process_instances":
				processIDs = append(processIDs, id)
			case "_workflow_executions":
				executionIDs = append(executionIDs, id)
			case "_action_executions":
				actionIDs = append(actionIDs, id)
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	// Notification SaaS intents carry explicit recipient and subject identity.
	statement, args, err := query.NewWorkspaceSelectBuilder(h.store.SQLRenderer, "_publication_outbox", workspace).Columns("id", "status", "intent_json").Where(query.Equal("publication_type", "notification.saas")).OrderBy(query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, raw string
		if err = rows.Scan(&id, &status, &raw); err != nil {
			return nil, err
		}
		var intent notificationmodel.NotificationIntent
		if json.Unmarshal([]byte(raw), &intent) != nil {
			return nil, fmt.Errorf("Runtime notification intent invalid")
		}
		covered := slices.Contains(intent.RecipientUserIDs, subject) || intent.SubjectType == "user" && intent.SubjectID == subject
		for _, ref := range resources {
			covered = covered || intent.SubjectType == ref.ObjectKey && intent.SubjectID == ref.RecordID
		}
		if covered {
			if lock && status == "sending" {
				return nil, fmt.Errorf("runtime.subject_evidence_busy: notification.saas")
			}
			items = append(items, rowReference{Table: "_publication_outbox", ID: id})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Table != items[j].Table {
			return items[i].Table < items[j].Table
		}
		return items[i].ID < items[j].ID
	})
	items = slices.Compact(items)
	if len(items) > 10000 {
		return nil, fmt.Errorf("Runtime subject evidence limit exceeded")
	}
	return items, nil
}

func (h *Handler) inventory(ctx context.Context, workspace, subject string) (plan, error) {
	if err := scope(ctx, workspace, subject); err != nil {
		return plan{}, err
	}
	resources, err := h.resolve(ctx, workspace, subject)
	if err != nil {
		return plan{}, err
	}
	resources = append(resources, recordmodel.SubjectRecordReference{ObjectKey: "identity_user", RecordID: subject})
	events, err := h.eventIDs(ctx, workspace, subject, resources)
	if err != nil {
		return plan{}, err
	}
	tx, err := h.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return plan{}, err
	}
	defer tx.Rollback()
	rows, err := h.collect(ctx, tx, workspace, subject, resources, events, false)
	if err != nil {
		return plan{}, err
	}
	if err = tx.Commit(); err != nil {
		return plan{}, err
	}
	return plan{WorkspaceID: workspace, SubjectID: subject, Resources: resources, EventIDs: events, Rows: rows}, nil
}

func (h *Handler) eventIDs(ctx context.Context, workspace, subject string, resources []recordmodel.SubjectRecordReference) ([]string, error) {
	if h.events == nil {
		return []string{}, nil
	}
	ids, err := h.events(ctx, workspace, subject, resources)
	if err != nil {
		return nil, err
	}
	if len(ids) > 10000 {
		return nil, fmt.Errorf("Runtime subject event limit exceeded")
	}
	sort.Strings(ids)
	return slices.Compact(ids), nil
}
func (h *Handler) PreviewSubject(ctx context.Context, workspace, subject string) (json.RawMessage, error) {
	p, err := h.inventory(ctx, workspace, subject)
	if err != nil {
		return nil, err
	}
	return json.Marshal(p.Rows)
}
func (h *Handler) ExportSubjectForRequest(ctx context.Context, _ string, workspace, subject string) (json.RawMessage, error) {
	p, err := h.inventory(ctx, workspace, subject)
	if err != nil {
		return nil, err
	}
	out := map[string][]map[string]any{}
	for _, ref := range p.Rows {
		statement, args, err := query.NewWorkspaceSelectBuilder(h.store.SQLRenderer, ref.Table, workspace).Projections(query.Project(query.AllColumns())).Where(query.Equal("id", ref.ID)).Build()
		if err != nil {
			return nil, err
		}
		rows, err := h.store.DB().QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			item := map[string]any{}
			for i, col := range columns {
				if b, ok := values[i].([]byte); ok {
					values[i] = string(b)
				}
				item[col] = values[i]
			}
			out[ref.Table] = append(out[ref.Table], item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(out)
}
func (*Handler) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("Runtime evidence erasure requires a persisted plan")
}
func (h *Handler) receipt(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace, request, subject string) (string, string, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(h.store.SQLRenderer, "_subject_evidence_erasure_receipts", workspace).Columns("subject_id", "plan_json", "result_json").Where(query.Equal("request_id", request)).Build()
	if err != nil {
		return "", "", err
	}
	var savedSubject, p, result string
	err = tx.QueryRowContext(ctx, statement, args...).Scan(&savedSubject, &p, &result)
	if err == nil && savedSubject != subject {
		err = fmt.Errorf("Runtime erasure receipt subject mismatch")
	}
	return p, result, err
}
func (h *Handler) PrepareSubjectErasure(ctx context.Context, request, workspace, subject string) (json.RawMessage, error) {
	if err := scope(ctx, workspace, subject); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request) == "" {
		return nil, fmt.Errorf("Runtime erasure request required")
	}
	if saved, _, err := h.receipt(ctx, h.store.DB(), workspace, request, subject); err == nil {
		return json.RawMessage(saved), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	resources, err := h.resolve(ctx, workspace, subject)
	if err != nil {
		return nil, err
	}
	resources = append(resources, recordmodel.SubjectRecordReference{ObjectKey: "identity_user", RecordID: subject})
	events, err := h.eventIDs(ctx, workspace, subject, resources)
	if err != nil {
		return nil, err
	}
	tx, err := h.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if saved, _, err := h.receipt(ctx, tx, workspace, request, subject); err == nil {
		return json.RawMessage(saved), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	items, err := h.collect(ctx, tx, workspace, subject, resources, events, true)
	if err != nil {
		return nil, err
	}
	p := plan{RequestID: request, WorkspaceID: workspace, SubjectID: subject, Resources: resources, EventIDs: events, Rows: items}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	fence := func(kind, object, id string) error {
		statement, args, err := query.NewWorkspaceSelectBuilder(h.store.SQLRenderer, "_subject_evidence_erasure_fences", workspace).Columns("request_id").Where(query.And(query.Equal("kind", kind), query.Equal("object_key", object), query.Equal("record_id", id))).Build()
		if err != nil {
			return err
		}
		var previous string
		err = tx.QueryRowContext(ctx, statement, args...).Scan(&previous)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		statement, args, err = query.NewWorkspaceInsertBuilder(h.store.SQLRenderer, "_subject_evidence_erasure_fences", workspace).Columns("kind", "object_key", "record_id", "request_id").Values(kind, object, id, request).Build()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, statement, args...)
		return err
	}
	if err = fence("subject", "", subject); err != nil {
		return nil, err
	}
	for _, ref := range resources {
		if err = fence("record", ref.ObjectKey, ref.RecordID); err != nil {
			return nil, err
		}
	}
	for _, ref := range items {
		if err = fence(ref.Table, "", ref.ID); err != nil {
			return nil, err
		}
	}

	for _, ref := range items {
		if ref.Table != "_workflow_process_instances" {
			continue
		}
		statement, args, err := query.NewWorkspaceUpdateBuilder(h.store.SQLRenderer, ref.Table, workspace).
			Set("status", "cancelled").Set("error_code", "runtime.subject_erased").Where(query.Equal("id", ref.ID)).Build()
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
			return nil, err
		}
	}
	// Cancel queued effects before another owner can erase the source record.
	for _, ref := range items {
		for _, s := range specs {
			if s.table != ref.Table || !s.fence {
				continue
			}
			builder := query.NewWorkspaceUpdateBuilder(h.store.SQLRenderer, ref.Table, workspace).Set("status", "failed").Set("lease_owner", "").Set("lease_expires_at", "").SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1)))
			if ref.Table == "_workflow_executions" {
				builder.Set("status", "cancelled").Set("next_run_at", "")
			}
			if ref.Table == "_publication_outbox" {
				builder.Set("next_attempt_at", "").Set("last_error_code", "runtime.subject_erased")
			}
			statement, args, err := builder.Where(query.Equal("id", ref.ID)).Build()
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
				return nil, err
			}
		}
	}
	statement, args, err := query.NewWorkspaceInsertBuilder(h.store.SQLRenderer, "_subject_evidence_erasure_receipts", workspace).Columns("request_id", "subject_id", "plan_json", "result_json").Values(request, subject, string(raw), "").Build()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return raw, nil
}
func (h *Handler) ErasePreparedSubject(ctx context.Context, request, workspace, subject string, raw json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if err := scope(ctx, workspace, subject); err != nil {
		return nil, err
	}
	if len(holds) > 0 {
		return nil, fmt.Errorf("Runtime evidence erasure blocked by legal hold")
	}
	var p plan
	if json.Unmarshal(raw, &p) != nil || request == "" || p.RequestID != request || p.WorkspaceID != workspace || p.SubjectID != subject {
		return nil, fmt.Errorf("Runtime evidence erasure plan scope mismatch")
	}
	tx, err := h.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	saved, result, err := h.receipt(ctx, tx, workspace, request, subject)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal([]byte(saved), raw) {
		return nil, fmt.Errorf("Runtime evidence plan differs from source receipt")
	}
	if result != "" {
		return json.RawMessage(result), nil
	}
	for _, ref := range p.Rows {
		var found *spec
		for i := range specs {
			if specs[i].table == ref.Table {
				found = &specs[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("Runtime evidence table invalid")
		}
		if len(found.set) == 0 {
			statement, args, err := query.NewWorkspaceDeleteBuilder(h.store.SQLRenderer, ref.Table, workspace).Where(query.Equal("id", ref.ID)).Build()
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
				return nil, err
			}
			continue
		}
		builder := query.NewWorkspaceUpdateBuilder(h.store.SQLRenderer, ref.Table, workspace)
		keys := make([]string, 0, len(found.set))
		for key := range found.set {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := found.set[key]
			if key == "idempotency_key" {
				digest := sha256.Sum256([]byte(ref.Table + ":" + ref.ID))
				value = "erased:" + hex.EncodeToString(digest[:])
			}
			builder.Set(key, value)
		}
		statement, args, err := builder.Where(query.Equal("id", ref.ID)).Build()
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
			return nil, err
		}
	}
	out, err := json.Marshal(map[string]any{"erased": true, "rows": len(p.Rows)})
	if err != nil {
		return nil, err
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(h.store.SQLRenderer, "_subject_evidence_erasure_receipts", workspace).Set("result_json", string(out)).Where(query.Equal("request_id", request)).Build()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

var _ contract.SubjectExecutionHandler = (*Handler)(nil)
var _ contract.PreparedSubjectErasureHandler = (*Handler)(nil)
