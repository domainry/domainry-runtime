package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
)

// SubjectEvidenceWriteAllowed is an adapter-private final SQL predicate. It
// protects cleaned rows and their parent processes even from cached workers.
func (s *RuntimeStore) SubjectEvidenceWriteAllowed(workspace, table, id string) query.Predicate {
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.And(query.Equal("kind", table), query.Equal("object_key", ""), query.Equal("record_id", id)))))
}

func (s *RuntimeStore) SubjectEvidenceRowWriteAllowed(workspace, table string) query.Predicate {
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.And(query.Equal("kind", table), query.Equal("object_key", ""),
		query.EqualExpressions(query.TableColumn("_subject_evidence_erasure_fences", "record_id"), query.TableColumn(table, "id"))))))
}

func (s *RuntimeStore) SubjectResourceWriteAllowed(workspace, object, id string) query.Predicate {
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.And(query.Equal("kind", "record"), query.Equal("object_key", object), query.Equal("record_id", id)))))
}

func (s *RuntimeStore) SubjectActorWriteAllowed(workspace, actor string) query.Predicate {
	if actor == "" {
		return query.AlwaysTrue()
	}
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.And(query.Equal("kind", "subject"), query.Equal("object_key", ""), query.Equal("record_id", actor)))))
}

// SubjectEvidenceInsertBuilder checks immutable ownership in the INSERT itself.
// ORM workspace INSERT does not support a SELECT source, so this adapter owns
// the workspace column explicitly. An aggregate seed returns exactly one row
// even when no fences exist, across SQLite, PostgreSQL and MySQL.
func (s *RuntimeStore) SubjectEvidenceInsertBuilder(workspace, table string, columns []string, values []any, extra ...query.Predicate) (*query.InsertBuilder, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("subject evidence workspace required")
	}
	predicate, err := s.subjectEvidenceWritePredicate(workspace, table, columns, values)
	if err != nil {
		return nil, err
	}
	if predicate == nil {
		predicate = query.AlwaysTrue()
	}
	for _, column := range columns {
		if strings.EqualFold(column, "workspace_id") {
			return nil, fmt.Errorf("subject evidence insert owns workspace_id")
		}
	}
	projections := []query.Projection{query.Project(query.Value(workspace))}
	for _, value := range values {
		projections = append(projections, query.Project(query.Value(value)))
	}
	seed := query.NewSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences").Projections(query.ProjectAs(query.CountAll(), "fence_count")).Where(query.AlwaysFalse())
	source := query.NewSelectFromSubquery(s.SQLRenderer, seed, "subject_insert_seed").Projections(projections...).Where(query.And(append([]query.Predicate{predicate}, extra...)...))
	return query.NewInsertBuilder(s.SQLRenderer, table).Columns(append([]string{"workspace_id"}, columns...)...).FromSelect(source), nil
}

// The correlation keeps a set mutation in one UPDATE and applies the fence to
// every selected record, including records not represented by a synthetic ID.
func (s *RuntimeStore) SubjectRecordWriteAllowed(workspace, object, actor string) query.Predicate {
	where := []query.Predicate{query.And(query.Equal("kind", "record"), query.Equal("object_key", object),
		query.EqualExpressions(query.QualifiedColumn("_subject_evidence_erasure_fences", "record_id"), query.QualifiedColumn(object, "id")))}
	if actor != "" {
		where = append(where, query.And(query.Equal("kind", "subject"), query.Equal("object_key", ""), query.Equal("record_id", actor)))
	}
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.Or(where...))))
}

func (s *RuntimeStore) GuardSubjectEvidenceWrite(ctx context.Context, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspace, table string, columns []string, values []any) error {
	predicate, err := s.subjectEvidenceWritePredicate(workspace, table, columns, values)
	if err != nil {
		return err
	}
	if predicate == nil {
		return nil
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).Columns("record_id").Where(query.Not(predicate)).Limit(1).Build()
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
	if len(columns) != len(values) {
		return nil, fmt.Errorf("subject evidence write shape invalid")
	}
	data := map[string]string{}
	for i, column := range columns {
		if values[i] != nil {
			data[column] = strings.TrimSpace(fmt.Sprint(values[i]))
		}
	}
	where := []query.Predicate{}
	add := func(kind, object, id string) {
		if id != "" {
			where = append(where, query.And(query.Equal("kind", kind), query.Equal("object_key", object), query.Equal("record_id", id)))
		}
	}
	add(table, "", data["id"])
	add("_workflow_process_instances", "", data["process_id"])
	for _, parent := range []string{"_action_executions", "_workflow_executions", "_record_mutation_executions"} {
		add(parent, "", data["request_ref"])
	}
	if data["record_id"] == "" {
		data["record_id"] = data["target_id"]
	}
	add("record", data["object_key"], data["record_id"])
	for _, column := range []string{"actor_id", "initiator_id", "created_by", "create_by", "update_by", "owner_user_id", "user_id", "assignee_user_id", "completed_by", "configured_by", "requested_by", "requester_user_id", "revoked_by"} {
		add("subject", "", data[column])
	}
	if len(where) == 0 {
		return nil, nil
	}
	return query.Not(query.ExistsSubquery(query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_subject_evidence_erasure_fences", workspace).
		Columns("record_id").Where(query.Or(where...)))), nil
}
