package record

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type updateEdgeRepository struct {
	recordrepository.RecordRepository
	record    recordmodel.Record
	found     bool
	getErr    error
	commitErr error
	commit    transactionmodel.RecordMutationCommit
	onCommit  func()
}

func (r *updateEdgeRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.getErr
}

func (r *updateEdgeRepository) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	r.commit = commit
	if r.onCommit != nil {
		r.onCommit()
	}
	return r.commitErr
}

func recordUpdateEdgeObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"}, {Key: "status", Type: "text"}, {Key: "age", Type: "number"}, {Key: "version", Type: "number"},
	}}
}

func recordUpdateEdgeDependencies(repository *updateEdgeRepository) RecordUpdateDependencies {
	return RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return recordUpdateEdgeObject(), nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	}
}

func TestUpdateDependencyFailuresAndCandidateGuards(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	stages := []string{
		"cancelled", "object", "not found", "expected timestamp", "expected version invalid", "expected version mismatch",
		"normalize patch", "identity", "pipeline validation", "pipeline defaults", "before", "cancelled after before",
		"transition before", "normalize candidate", "write scope", "relations", "policies",
		"self effects", "unique", "duplicate", "workflow", "commit", "cancelled after commit",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", UpdatedAt: "revision-2", Data: map[string]any{
				"name": "Before", "status": "open", "age": float64(20), "version": float64(2),
			}}}
			dependencies := recordUpdateEdgeDependencies(repository)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			patch := map[string]any{"name": "After"}
			wantCode := ""

			switch stage {
			case "cancelled":
				cancel()
			case "object":
				dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{}, failure
				}
			case "not found":
				repository.found = false
				wantCode = "backend.record.not_found"
			case "expected timestamp":
				patch["expected_updated_at"] = "revision-1"
				wantCode = "backend.record.version_conflict"
			case "expected version invalid":
				patch["expected_version"] = "invalid"
				wantCode = "backend.record.expected_version_invalid"
			case "expected version mismatch":
				patch["expected_version"] = 1
				wantCode = "backend.record.version_conflict"
			case "normalize patch":
				patch = map[string]any{"age": "invalid"}
				wantCode = "backend.validation.number"
			case "identity":
				dependencies.ApplyScopeOwnerFacts = func(context.Context, string, definitionmodel.ObjectSchema, map[string]any, string) error {
					return failure
				}
			case "pipeline validation":
				dependencies.ValidatePipeline = func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "pipeline defaults":
				dependencies.ApplyPipelineDefaults = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error {
					return failure
				}
			case "before":
				dependencies.RunBefore = func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "cancelled after before":
				dependencies.RunBefore = func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
					cancel()
					return nil
				}
			case "transition before":
				patch = map[string]any{"status": "closed"}
				calls := 0
				dependencies.RunBefore = func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
					calls++
					if calls == 2 {
						return failure
					}
					return nil
				}
			case "normalize candidate":
				dependencies.RunBefore = func(_ context.Context, _, _, _ string, _, _ map[string]any, candidate map[string]any, _ principalmodel.Principal) error {
					candidate["age"] = "invalid"
					return nil
				}
				wantCode = "backend.validation.number"
			case "write scope":
				dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }
				wantCode = "backend.record.owner_write_denied"
			case "relations":
				dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "policies":
				dependencies.ValidatePolicies = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
					return failure
				}
			case "self effects":
				dependencies.ApplySelfEffects = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error) {
					return false, failure
				}
			case "unique":
				dependencies.ValidateUnique = func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error {
					return failure
				}
			case "duplicate":
				dependencies.ValidateDuplicate = func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error {
					return failure
				}
			case "workflow":
				dependencies.UpdatedTriggers = func(string, map[string]any, map[string]any) []string { return []string{"record_updated:customer.name"} }
				dependencies.PrepareWorkflow = func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
					return nil, failure
				}
			case "commit":
				repository.commitErr = failure
				wantCode = "backend.internal"
			case "cancelled after commit":
				repository.onCommit = cancel
			}

			updated, err := NewRecordUpdateApplicationService(dependencies).Update(ctx, "customer", "customer-1", patch, principal)
			if wantCode != "" {
				if apperror.CodeOf(err) != wantCode {
					t.Fatalf("updated=%#v err=%v code=%q want=%q", updated, err, apperror.CodeOf(err), wantCode)
				}
			} else if stage == "cancelled" || stage == "cancelled after before" || stage == "cancelled after commit" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("updated=%#v err=%v", updated, err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("updated=%#v err=%v want=%v", updated, err, failure)
			}

			committedBeforeFailure := stage == "commit" || stage == "cancelled after commit"
			if (repository.commit.Operation != "") != committedBeforeFailure {
				t.Fatalf("commit=%#v committedBeforeFailure=%v", repository.commit, committedBeforeFailure)
			}
		})
	}
}

func TestUpdateSelfEffectRevalidatesChangedCandidate(t *testing.T) {
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Before", "status": "open", "age": float64(20), "version": float64(2)}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	validationCalls := 0
	dependencies.ApplySelfEffects = func(_ context.Context, _ definitionmodel.ObjectSchema, _ map[string]any, next map[string]any, _ string, _ principalmodel.Principal) (bool, error) {
		next["name"] = "Self Effect"
		return true, nil
	}
	dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
		validationCalls++
		return nil
	}
	updated, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "customer-1", map[string]any{"name": "After"}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}}))
	if err != nil || updated.Data["name"] != "Self Effect" || validationCalls != 2 {
		t.Fatalf("updated=%#v validationCalls=%d err=%v", updated, validationCalls, err)
	}
}
