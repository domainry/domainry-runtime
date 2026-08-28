package record

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestRecordDeleteScopeAndLifecycleInvariantFailures(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
	dependencies := recordDeleteEdgeDependencies(repository, object)
	scopeErr := errors.New("scope unavailable")
	dependencies.CanAccessScope = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error) {
		return false, scopeErr
	}
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), object.Key, "customer-1", principal); !errors.Is(err, scopeErr) {
		t.Fatalf("scope err=%v", err)
	}
	softOnly := definitionmodel.ObjectSchema{Key: "customer", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly}, Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
	if err := NewRecordDeleteApplicationService(recordDeleteEdgeDependencies(repository, softOnly)).Delete(t.Context(), softOnly.Key, "customer-1", principal); apperror.CodeOf(err) != "backend.record.lifecycle_soft_delete_fields_required" {
		t.Fatalf("soft-delete invariant err=%v", err)
	}
}

func TestRecordSoftDeleteWithExplicitRevision(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	object := definitionmodel.ObjectSchema{
		Key:             "customer",
		LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly},
		Fields: []definitionmodel.FieldSchema{
			{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"},
		},
	}
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", UpdatedAt: "revision-1", Data: map[string]any{"status": "active"}}}
	if err := NewRecordDeleteApplicationService(recordDeleteEdgeDependencies(repository, object)).DeleteExpected(t.Context(), object.Key, "customer-1", "revision-1", principal); err != nil {
		t.Fatal(err)
	}
}

func TestRecordDeleteRelationCycleAndSetNullPlannerFailure(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	root := definitionmodel.ObjectSchema{Key: "root", Fields: []definitionmodel.FieldSchema{{Key: "parent_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "cascade"}}}}
	repository := &deleteRepositoryProbe{
		records: map[string]recordmodel.Record{"root:root-1": {ID: "root-1", Data: map[string]any{"parent_id": "root-1"}}},
		lists:   map[string][]recordmodel.Record{"root": {{ID: "root-1", Data: map[string]any{"parent_id": "root-1"}}}},
	}
	dependencies := recordDeleteEdgeDependencies(repository, root)
	dependencies.Relations = recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"root": root}
	})
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), root.Key, "root-1", principal); err != nil {
		t.Fatal(err)
	}
	note := definitionmodel.ObjectSchema{Key: "note", Fields: []definitionmodel.FieldSchema{{Key: "root_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "set_null"}}}}
	repository = &deleteRepositoryProbe{
		records: map[string]recordmodel.Record{"root:root-1": {ID: "root-1", Data: map[string]any{}}},
		lists:   map[string][]recordmodel.Record{"note": {{ID: "note-1", Data: map[string]any{"root_id": "root-1"}}}},
	}
	plannerErr := errors.New("set-null planning failed")
	dependencies = recordDeleteEdgeDependencies(repository, root)
	dependencies.Relations = recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"root": root, "note": note}
	})
	dependencies.PlanUpdateReference = func(context.Context, recordservice.RecordDeleteReference, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, plannerErr
	}
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), root.Key, "root-1", principal); !errors.Is(err, plannerErr) {
		t.Fatalf("planner err=%v", err)
	}
}

func TestRecordHardDeleteOutboxAndCanonicalPlanRejection(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
	dependencies := recordDeleteEdgeDependencies(repository, object)
	dependencies.AfterOutbox = func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage {
		return []integrationmodel.IntegrationOutboxMessage{{ID: "outbox"}}
	}
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), object.Key, "customer-1", principal); err != nil || len(repository.commits) != 1 || len(repository.commits[0].Outbox) != 1 {
		t.Fatalf("commits=%+v err=%v", repository.commits, err)
	}
	appendOnly := object
	appendOnly.LifecyclePolicy = &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
	if err := NewRecordDeleteApplicationService(recordDeleteEdgeDependencies(repository, appendOnly)).Delete(t.Context(), appendOnly.Key, "customer-1", principal); err == nil {
		t.Fatal("append-only delete plan accepted")
	}
	immutableSoft := definitionmodel.ObjectSchema{
		Key:             "customer",
		LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"active"}},
		Fields:          []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}},
	}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}}
	if err := NewRecordDeleteApplicationService(recordDeleteEdgeDependencies(repository, immutableSoft)).Delete(t.Context(), immutableSoft.Key, "customer-1", principal); err == nil {
		t.Fatal("immutable soft-delete update plan accepted")
	}
}
