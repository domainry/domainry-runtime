package record

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type recordUpdateEmptyCodeError struct{}

func (recordUpdateEmptyCodeError) Error() string                  { return "empty code" }
func (recordUpdateEmptyCodeError) ErrorCode() string              { return " " }
func (recordUpdateEmptyCodeError) ErrorParams() map[string]string { return nil }

func recordUpdateFinalPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
}

func TestRecordUpdateSchedulerAndOptimisticConditionEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "run-1", UpdatedAt: "revision", Data: map[string]any{"version": "invalid"}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return definitionmodel.ObjectSchema{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}}, nil
	}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "job_run", "run-1", map[string]any{}, principal); apperror.CodeOf(err) != "backend.scheduler.runtime_api_required" {
		t.Fatalf("scheduler guard error = %v", err)
	}

	dependencies = recordUpdateEdgeDependencies(repository)
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "run-1", map[string]any{"expected_updated_at": " ", "expected_version": 1}, principal); apperror.CodeOf(err) != "backend.record.version_conflict" {
		t.Fatalf("invalid actual version error = %v", err)
	}
}

func TestRecordUpdatePipelineStageAndOptionalShortCircuitEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	object := definitionmodel.ObjectSchema{Key: "pipeline_item", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"}, {Key: "current_stage", Type: "text"}, {Key: "last_transition_at", Type: "datetime"}, {Key: "version", Type: "number"}, {Key: "status", Type: "text"},
	}}
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "item-1", UpdatedAt: "revision", Data: map[string]any{"current_stage": "old", "version": "invalid", "status": "open"}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return object, nil
	}
	dependencies.CanAccess = nil
	dependencies.CanWrite = nil
	dependencies.Now = func() time.Time { return time.Date(2026, 7, 20, 7, 8, 9, 0, time.UTC) }
	updated, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), object.Key, "item-1", map[string]any{"current_stage": "new"}, principal)
	if err != nil || updated.Data["version"] != float64(2) {
		t.Fatalf("pipeline update=%#v err=%v", updated, err)
	}
	repository.record = recordmodel.Record{ID: "item-2", UpdatedAt: "revision", Data: map[string]any{"name": "before", "current_stage": "old", "version": float64(2), "status": "open"}}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), object.Key, "item-2", map[string]any{"name": "after"}, principal); err != nil {
		t.Fatalf("pipeline non-stage patch error = %v", err)
	}
	repository.record = recordmodel.Record{ID: "item-3", UpdatedAt: "revision", Data: map[string]any{"current_stage": "old", "version": float64(2), "status": "open"}}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), object.Key, "item-3", map[string]any{"current_stage": "new"}, principal); err != nil {
		t.Fatalf("pipeline valid-version transition error = %v", err)
	}

	// A transition candidate with no before-hook must still proceed safely.
	regularRepository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "before", "status": "open", "version": float64(1)}}}
	regular := recordUpdateEdgeDependencies(regularRepository)
	regular.RunBefore = nil
	regular.AfterOutbox = func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage {
		return nil
	}
	regular.UpdatedTriggers = func(string, map[string]any, map[string]any) []string {
		return []string{"record_updated:customer.status"}
	}
	regular.PrepareWorkflow = nil
	if _, err := NewRecordUpdateApplicationService(regular).Update(t.Context(), "customer", "customer-1", map[string]any{"status": "closed"}, principal); err != nil {
		t.Fatalf("optional transition update error = %v", err)
	}
	regularRepository.record = recordmodel.Record{ID: "customer-2", Data: map[string]any{"name": "before", "status": "open", "version": float64(1)}}
	regular.UpdatedTriggers = nil
	if _, err := NewRecordUpdateApplicationService(regular).Update(t.Context(), "customer", "customer-2", map[string]any{"name": "after"}, principal); err != nil {
		t.Fatalf("non-transition outbox update error = %v", err)
	}
}

func TestRecordUpdateSecondPassFailuresAndValidationEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	baseRecord := recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "before", "status": "open", "age": float64(20), "version": float64(1)}}
	edgeErr := errors.New("second pass failed")

	repository := &updateEdgeRepository{found: true, record: baseRecord}
	dependencies := recordUpdateEdgeDependencies(repository)
	derivedCalls := 0
	dependencies.ApplyScopeOwnerFacts = func(context.Context, string, definitionmodel.ObjectSchema, map[string]any, string) error {
		derivedCalls++
		if derivedCalls == 2 {
			return edgeErr
		}
		return nil
	}
	dependencies.ApplySelfEffects = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error) {
		return true, nil
	}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "customer-1", map[string]any{"name": "after"}, principal); !errors.Is(err, edgeErr) {
		t.Fatalf("second derivation error = %v", err)
	}

	repository = &updateEdgeRepository{found: true, record: baseRecord}
	dependencies = recordUpdateEdgeDependencies(repository)
	relationCalls := 0
	dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
		relationCalls++
		if relationCalls == 2 {
			return edgeErr
		}
		return nil
	}
	dependencies.ApplySelfEffects = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error) {
		return true, nil
	}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "customer-1", map[string]any{"name": "after"}, principal); !errors.Is(err, edgeErr) {
		t.Fatalf("second validation error = %v", err)
	}
	repository = &updateEdgeRepository{found: true, record: baseRecord}
	dependencies = recordUpdateEdgeDependencies(repository)
	dependencies.ApplySelfEffects = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error) {
		return false, nil
	}
	if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "customer-1", map[string]any{"name": "after"}, principal); err != nil {
		t.Fatalf("unchanged self-effect update error = %v", err)
	}

	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{})
	required := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}
	if err := service.validateCandidate(t.Context(), required.Key, required, "customer-1", map[string]any{}, map[string]any{}, map[string]any{}, principal, principal, true); apperror.CodeOf(err) == "" {
		t.Fatalf("direct candidate validation error = %v", err)
	}
	service.dependencies.CanWrite = nil
	valid := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	if err := service.validateCandidate(t.Context(), valid.Key, valid, "customer-1", map[string]any{}, map[string]any{"name": "ok"}, map[string]any{}, principal, principal, false); err != nil {
		t.Fatalf("nil write guard validation error = %v", err)
	}

	if apperror.CodeOf(recordUpdateErrorFrom(apperror.KindBadRequest, recordUpdateEmptyCodeError{})) != "backend.bad_request" {
		t.Fatal("empty coded error did not use fallback")
	}
	if appErr := recordUpdateError(apperror.KindBadRequest, "code", nil, " ", "ignored").(*apperror.AppError); appErr.Params != nil {
		t.Fatalf("blank parameter key produced params=%#v", appErr.Params)
	}
}

func TestRecordUpdateRelationValidationOnlyReceivesChangedReferences(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	object := definitionmodel.ObjectSchema{Key: "sales_order", Fields: []definitionmodel.FieldSchema{
		{Key: "created_by_identity_user_id", Type: "relation"},
		{Key: "status", Type: "select"},
	}}
	var validated map[string]any
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		CanAccessScope: func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error) {
			return true, nil
		},
		ValidateRelations: func(_ context.Context, _ definitionmodel.ObjectSchema, data map[string]any, _ principalmodel.Principal) error {
			validated = data
			return nil
		},
	})
	before := map[string]any{"created_by_identity_user_id": "manager_demo", "status": "draft"}
	unchangedRelation := map[string]any{"created_by_identity_user_id": "manager_demo", "status": "paid"}
	if err := service.validateCandidate(t.Context(), object.Key, object, "order-1", before, unchangedRelation, map[string]any{"status": "paid"}, principal, principal, false); err != nil {
		t.Fatal(err)
	}
	if len(validated) != 0 {
		t.Fatalf("unchanged relation was revalidated: %#v", validated)
	}

	changedRelation := map[string]any{"created_by_identity_user_id": "other_manager", "status": "paid"}
	if err := service.validateCandidate(t.Context(), object.Key, object, "order-1", before, changedRelation, map[string]any{"created_by_identity_user_id": "other_manager"}, principal, principal, false); err != nil {
		t.Fatal(err)
	}
	if got := validated["created_by_identity_user_id"]; got != "other_manager" || len(validated) != 1 {
		t.Fatalf("changed relation validation data=%#v", validated)
	}
}
