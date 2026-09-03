package record

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type recordMutationTargetRepositoryProbe struct {
	listQuery recordmodel.RecordListQuery
	getCalls  int
}

func (r *recordMutationTargetRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.listQuery = query
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "candidate"}}}, nil
}

func (r *recordMutationTargetRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	r.getCalls++
	return recordmodel.Record{}, false, nil
}

func (*recordMutationTargetRepositoryProbe) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (*recordMutationTargetRepositoryProbe) UpdateRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (*recordMutationTargetRepositoryProbe) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return false, nil
}

func (*recordMutationTargetRepositoryProbe) DeleteRecord(context.Context, string, definitionmodel.ObjectSchema, string) error {
	return nil
}

func (*recordMutationTargetRepositoryProbe) CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error {
	return nil
}

func (*recordMutationTargetRepositoryProbe) CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error {
	return nil
}

func (*recordMutationTargetRepositoryProbe) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return false, nil
}

func TestRecordMutationTargetLoaderTreatsClientIDAsScopedCandidate(t *testing.T) {
	repository := &recordMutationTargetRepositoryProbe{}
	scope := &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"server-user"}}
	record, found, err := recordRepositoryMutationTargetLoader(repository)(t.Context(), "workspace", definitionmodel.ObjectSchema{Key: "order"}, "candidate", scope)
	if err != nil || !found || record.ID != "candidate" {
		t.Fatalf("scoped candidate lookup record=%#v found=%v err=%v", record, found, err)
	}
	ids, ok := repository.listQuery.Filters["id__in"].([]any)
	if repository.getCalls != 0 || !ok || len(ids) != 1 || ids[0] != "candidate" {
		t.Fatalf("client ID bypassed scoped list query: query=%#v get_calls=%d", repository.listQuery, repository.getCalls)
	}
	if repository.listQuery.AuthorizationMode != recordmodel.RecordQueryAuthorizationPredicate || repository.listQuery.RootObjectKey != "order" || repository.listQuery.ScopeExpression != scope || !repository.listQuery.SkipTotal {
		t.Fatalf("candidate lookup omitted authorization scope: %#v", repository.listQuery)
	}
}

func TestRecordMutationTargetLoaderUsesOnlyWorkspaceAndCandidateIDForAll(t *testing.T) {
	repository := &recordMutationTargetRepositoryProbe{}
	record, found, err := recordRepositoryMutationTargetLoader(repository)(t.Context(), "workspace", definitionmodel.ObjectSchema{Key: "order"}, "candidate", nil)
	if err != nil || !found || record.ID != "candidate" {
		t.Fatalf("all candidate lookup record=%#v found=%v err=%v", record, found, err)
	}
	ids, ok := repository.listQuery.Filters["id__in"].([]any)
	if repository.getCalls != 0 || !ok || len(ids) != 1 || ids[0] != "candidate" {
		t.Fatalf("client ID bypassed tenant list query: query=%#v get_calls=%d", repository.listQuery, repository.getCalls)
	}
	if repository.listQuery.AuthorizationMode != recordmodel.RecordQueryAuthorizationUnrestricted || repository.listQuery.RootObjectKey != "" || repository.listQuery.ScopeExpression != nil {
		t.Fatalf("all unexpectedly added a data-scope predicate: %#v", repository.listQuery)
	}
}
