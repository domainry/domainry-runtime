package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
)

const (
	sharedSubjectRequestsTable          = "_subject_requests"
	lifecycleSubjectExecutionStepsTable = "_subject_steps"
	lifecycleSubjectOwner               = "lifecycle"
	lifecycleEraseFenceOperation        = "erase_fence"
	runtimeEvidenceSubjectOwner         = "runtime_evidence"
	runtimeEvidenceErasePlanOperation   = "erase_plan"
)

func (s *RuntimeStore) subjectErasureFenceRequests(workspace, request, subject string) *query.SelectBuilder {
	requestIDs := query.NewWorkspaceSelectBuilder(s.SQLRenderer, sharedSubjectRequestsTable, workspace).Columns("id").Where(query.And(
		query.NotEqual("request_type", "external_erasure"),
		query.Equal("kind", "erase"),
		query.Equal("resolved_identity", subject),
	))
	predicates := []query.Predicate{
		query.Equal("owner", lifecycleSubjectOwner),
		query.Equal("operation", lifecycleEraseFenceOperation),
		query.InSubquery("request_id", requestIDs),
	}
	if strings.TrimSpace(request) != "" {
		predicates = append(predicates, query.Equal("request_id", request))
	}
	return query.NewWorkspaceSelectBuilder(s.SQLRenderer, lifecycleSubjectExecutionStepsTable, workspace).
		Columns("request_id").Where(query.And(predicates...))
}

func (s *RuntimeStore) SubjectErasureFenceExists(workspace, subject string) query.Predicate {
	return query.ExistsSubquery(s.subjectErasureFenceRequests(workspace, "", subject))
}

func (s *RuntimeStore) SubjectErasureFenceMatches(workspace, request, subject string) query.Predicate {
	return query.ExistsSubquery(s.subjectErasureFenceRequests(workspace, request, subject))
}

type subjectPlanRowReference struct {
	Table string `json:"table"`
	ID    string `json:"id"`
}

type subjectPlanRecordReference struct {
	ObjectKey string `json:"object_key"`
	RecordID  string `json:"record_id"`
}

// SubjectEvidenceWriteAllowed protects one Runtime evidence row by consulting
// the Runtime owner's plan persisted in Lifecycle's shared execution-step
// journal. Runtime does not own another fence table.
func (s *RuntimeStore) SubjectEvidenceWriteAllowed(workspace, table, id string) query.Predicate {
	if !s.SubjectLifecyclePersistenceBound() || strings.TrimSpace(id) == "" {
		return query.AlwaysTrue()
	}
	return query.Not(s.subjectPlanContains(workspace, subjectPlanRowReference{Table: table, ID: id}))
}

func (s *RuntimeStore) SubjectResourceWriteAllowed(workspace, object, id string) query.Predicate {
	workspace, object, id = strings.TrimSpace(workspace), strings.TrimSpace(object), strings.TrimSpace(id)
	if !s.SubjectLifecyclePersistenceBound() || strings.TrimSpace(object) == "" || strings.TrimSpace(id) == "" {
		return query.AlwaysTrue()
	}
	return query.Not(s.subjectPlanContains(workspace, subjectPlanRecordReference{ObjectKey: object, RecordID: id}))
}

func (s *RuntimeStore) SubjectActorWriteAllowed(workspace, actor string) query.Predicate {
	workspace, actor = strings.TrimSpace(workspace), strings.TrimSpace(actor)
	if !s.SubjectLifecyclePersistenceBound() || strings.TrimSpace(actor) == "" {
		return query.AlwaysTrue()
	}
	return query.Not(s.SubjectErasureFenceExists(workspace, actor))
}

func (s *RuntimeStore) SubjectRecordWriteAllowed(workspace, object, recordID, actor string) query.Predicate {
	if !s.SubjectLifecyclePersistenceBound() {
		return query.AlwaysTrue()
	}
	return query.And(s.SubjectResourceWriteAllowed(workspace, object, recordID), s.SubjectActorWriteAllowed(workspace, actor))
}

// SubjectEvidenceInsertBuilder checks immutable ownership in the INSERT itself.
// ORM workspace INSERT does not support a SELECT source, so this adapter owns
// the workspace column explicitly. An aggregate seed returns exactly one row
// even when no Lifecycle steps exist, across SQLite, PostgreSQL and MySQL.
func (s *RuntimeStore) SubjectEvidenceInsertBuilder(workspace, table string, columns []string, values []any, extra ...query.Predicate) (*query.InsertBuilder, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("subject evidence workspace required")
	}
	for _, column := range columns {
		if strings.EqualFold(column, "workspace_id") {
			return nil, fmt.Errorf("subject evidence insert owns workspace_id")
		}
	}
	if !s.SubjectLifecyclePersistenceBound() {
		return query.NewWorkspaceInsertBuilder(s.SQLRenderer, table, workspace).Columns(columns...).Values(values...), nil
	}
	predicate, err := s.subjectEvidenceWritePredicate(workspace, table, columns, values)
	if err != nil {
		return nil, err
	}
	if predicate == nil {
		predicate = query.AlwaysTrue()
	}
	projections := []query.Projection{query.Project(query.Value(workspace))}
	for _, value := range values {
		projections = append(projections, query.Project(query.Value(value)))
	}
	seed := query.NewWorkspaceSelectBuilder(s.SQLRenderer, lifecycleSubjectExecutionStepsTable, workspace).
		Projections(query.ProjectAs(query.CountAll(), "step_count")).Where(query.AlwaysFalse())
	source := query.NewSelectFromSubquery(s.SQLRenderer, seed, "subject_insert_seed").
		Projections(projections...).Where(query.And(append([]query.Predicate{predicate}, extra...)...))
	return query.NewInsertBuilder(s.SQLRenderer, table).
		Columns(append([]string{"workspace_id"}, columns...)...).FromSelect(source), nil
}

func (s *RuntimeStore) GuardSubjectEvidenceWrite(ctx context.Context, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspace, table string, columns []string, values []any) error {
	if !s.SubjectLifecyclePersistenceBound() {
		return nil
	}
	predicate, err := s.subjectEvidenceWritePredicate(workspace, table, columns, values)
	if err != nil {
		return err
	}
	if predicate == nil {
		return nil
	}
	return s.guardSubjectWriteAllowed(ctx, executor, workspace, predicate)
}

// GuardSubjectRecordsWrite blocks a set mutation before its UPDATE executes.
// The caller must run this guard and the mutation in the same serializable
// transaction so a concurrently prepared erasure cannot slip between them.
func (s *RuntimeStore) GuardSubjectRecordsWrite(ctx context.Context, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspace, object string, recordIDs []string, actor string) error {
	if !s.SubjectLifecyclePersistenceBound() {
		return nil
	}
	allowed := []query.Predicate{s.SubjectActorWriteAllowed(workspace, actor)}
	for _, id := range recordIDs {
		if id = strings.TrimSpace(id); id != "" {
			allowed = append(allowed, s.SubjectResourceWriteAllowed(workspace, object, id))
		}
	}
	return s.guardSubjectWriteAllowed(ctx, executor, workspace, query.And(allowed...))
}

func (s *RuntimeStore) guardSubjectWriteAllowed(ctx context.Context, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspace string, allowed query.Predicate) error {
	seed := query.NewWorkspaceSelectBuilder(s.SQLRenderer, lifecycleSubjectExecutionStepsTable, workspace).
		Projections(query.ProjectAs(query.CountAll(), "step_count")).Where(query.AlwaysFalse())
	statement, args, err := query.NewSelectFromSubquery(s.SQLRenderer, seed, "subject_write_guard").
		Columns("step_count").Where(query.Not(allowed)).Limit(1).Build()
	if err != nil {
		return err
	}
	rows, err := executor.QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("runtime.subject_erased")
	}
	return rows.Err()
}

func (s *RuntimeStore) subjectEvidenceWritePredicate(workspace, table string, columns []string, values []any) (query.Predicate, error) {
	if !s.SubjectLifecyclePersistenceBound() {
		return nil, nil
	}
	if len(columns) != len(values) {
		return nil, fmt.Errorf("subject evidence write shape invalid")
	}
	data := map[string]string{}
	for i, column := range columns {
		if values[i] != nil {
			data[column] = strings.TrimSpace(fmt.Sprint(values[i]))
		}
	}
	blocked := []query.Predicate{}
	addRow := func(rowTable, id string) {
		if strings.TrimSpace(rowTable) != "" && strings.TrimSpace(id) != "" {
			blocked = append(blocked, s.subjectPlanContains(workspace, subjectPlanRowReference{Table: rowTable, ID: id}))
		}
	}
	addRecord := func(object, id string) {
		if strings.TrimSpace(object) != "" && strings.TrimSpace(id) != "" {
			blocked = append(blocked, s.subjectPlanContains(workspace, subjectPlanRecordReference{ObjectKey: object, RecordID: id}))
		}
	}
	addActor := func(actor string) {
		if strings.TrimSpace(actor) != "" {
			blocked = append(blocked, s.SubjectErasureFenceExists(workspace, actor))
		}
	}

	addRow(table, data["id"])
	addRow("_workflow_process_instances", data["process_id"])
	for _, parent := range []string{"_workflow_executions", "_operations"} {
		addRow(parent, data["request_ref"])
	}
	if table == "_operations" && (data["owner"] == "record" || data["owner"] == "action") {
		addRecord(data["resource_type"], data["resource_id"])
	}
	if data["record_id"] == "" {
		data["record_id"] = data["target_id"]
	}
	addRecord(data["object_key"], data["record_id"])
	for _, column := range []string{"actor_id", "initiator_id", "created_by", "create_by", "update_by", "owner_user_id", "user_id", "assignee_user_id", "completed_by", "configured_by", "requested_by", "requester_user_id", "revoked_by"} {
		addActor(data[column])
	}
	if len(blocked) == 0 {
		return nil, nil
	}
	return query.Not(query.Or(blocked...)), nil
}

func (s *RuntimeStore) subjectPlanContains(workspace string, reference any) query.Predicate {
	raw, _ := json.Marshal(reference)
	escaped := strings.NewReplacer("~", "~~", "%", "~%", "_", "~_").Replace(string(raw))
	return query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, lifecycleSubjectExecutionStepsTable, workspace).
		Columns("request_id").Where(query.And(
		query.Equal("owner", runtimeEvidenceSubjectOwner),
		query.Equal("operation", runtimeEvidenceErasePlanOperation),
		query.LikeEscaped("payload_json", "%"+escaped+"%"),
	)))
}
