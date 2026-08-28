package record

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func recordCreateEdgeDependencies(repository *createRepositoryProbe) RecordCreateDependencies {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	return RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite:    func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		NewRecordID: func(string) string { return "customer-1" },
	}
}

func TestCreateClaimedUsesExistingClaimWithoutBeginningAnother(t *testing.T) {
	repository := &createRepositoryProbe{}
	executions := &createExecutionProbe{}
	dependencies := recordCreateEdgeDependencies(repository)
	dependencies.ExecutionRuntime = recordruntime.NewRecordMutationExecutionRuntime(executions)
	service := NewRecordCreateApplicationService(dependencies)
	claim := recordmodel.RecordMutationClaimResult{
		Decision: idempotency.DecisionAcquired,
		Execution: recordmodel.RecordMutationExecution{
			ID: "execution-1", WorkspaceID: "workspace-a", LeaseOwner: "request-a", FencingToken: 7,
		},
	}

	record, err := service.CreateClaimed(t.Context(), "customer", map[string]any{"name": "Acme"}, claim, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "customer-1" || executions.beginCalls != 0 || executions.commitCalls != 1 || executions.commit.Record.ID != record.ID || repository.commit.Operation != "" {
		t.Fatalf("record=%#v begin=%d commit=%d execution commit=%#v repository commit=%#v", record, executions.beginCalls, executions.commitCalls, executions.commit, repository.commit)
	}
}

func TestCreateIdempotentRequiresExecutionRuntime(t *testing.T) {
	service := NewRecordCreateApplicationService(recordCreateEdgeDependencies(&createRepositoryProbe{}))
	_, err := service.CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "create-key", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
	}
}

func TestCreatePrefersRelationAwareCandidateScopeAuthorization(t *testing.T) {
	repository := &createRepositoryProbe{}
	dependencies := recordCreateEdgeDependencies(repository)
	fallbackCalled := false
	dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool {
		fallbackCalled = true
		return false
	}
	dependencies.CanWriteCandidate = func(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
		if principal.WorkspaceID != "workspace-a" || object.Key != "customer" || candidate.ID != "customer-1" || candidate.Data["name"] != "Acme" {
			t.Fatalf("principal=%#v object=%#v candidate=%#v", principal, object, candidate)
		}
		return true, nil
	}
	record, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), "customer", map[string]any{"name": "Acme"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil || record.ID != "customer-1" || fallbackCalled {
		t.Fatalf("record=%#v fallback=%v err=%v", record, fallbackCalled, err)
	}

	dependencies.CanWriteCandidate = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error) {
		return false, nil
	}
	if _, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), "customer", map[string]any{"name": "Acme"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); apperror.CodeOf(err) != "backend.record.owner_write_denied" {
		t.Fatalf("candidate scope denial err=%v", err)
	}
}

func TestCreateDependencyFailuresStopBeforeCommit(t *testing.T) {
	stages := []string{
		"cancelled", "object", "identity", "normalize", "pipeline validation", "pipeline defaults",
		"write scope", "replay", "before", "cancelled after before", "relations",
		"policies", "unique", "duplicate", "workflow",
	}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})

	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			repository := &createRepositoryProbe{}
			dependencies := recordCreateEdgeDependencies(repository)
			failure := errors.New(stage + " failed")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			data := map[string]any{"name": "Acme"}
			wantCode := ""

			switch stage {
			case "cancelled":
				cancel()
			case "object":
				dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{}, failure
				}
			case "identity":
				dependencies.ApplyScopeOwnerFacts = func(context.Context, string, definitionmodel.ObjectSchema, map[string]any, string) error {
					return failure
				}
			case "normalize":
				dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "age", Type: "number"}}}, nil
				}
				data = map[string]any{"age": "not-a-number"}
				wantCode = "backend.validation.number"
			case "pipeline validation":
				dependencies.ValidatePipeline = func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "pipeline defaults":
				dependencies.ApplyPipelineDefaults = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error {
					return failure
				}
			case "write scope":
				dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }
				wantCode = "backend.record.owner_write_denied"
			case "replay":
				dependencies.FindReplay = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
					return recordmodel.Record{}, false, failure
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
			case "relations":
				dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "policies":
				dependencies.ValidatePolicies = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
					return failure
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
				dependencies.PrepareWorkflow = func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
					return nil, failure
				}
			}

			_, err := NewRecordCreateApplicationService(dependencies).Create(ctx, "customer", data, principal)
			if wantCode != "" {
				if apperror.CodeOf(err) != wantCode {
					t.Fatalf("err=%v code=%q want=%q", err, apperror.CodeOf(err), wantCode)
				}
			} else if stage == "cancelled" || stage == "cancelled after before" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("err=%v want wrapped/same %v", err, failure)
			}

			committedBeforeFailure := stage == "identity sync"
			if (repository.commit.Operation != "") != committedBeforeFailure {
				t.Fatalf("commit=%#v committedBeforeFailure=%v", repository.commit, committedBeforeFailure)
			}
		})
	}
}

func TestPlanCreateMutationCoversInputAuthorizationReplayAndPlannerEdges(t *testing.T) {
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}})
	failure := errors.New("dependency failed")

	t.Run("object lookup", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{}, failure
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("scheduler runtime object", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}}, nil
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "job_run", nil, "", principal)
		if apperror.CodeOf(err) != "backend.scheduler.runtime_api_required" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("nil data and explicit record id", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		plan, candidate, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, " customer-explicit ", principal)
		if err != nil || candidate.ID != " customer-explicit " || plan.CanonicalCommit().Record.ID != candidate.ID {
			t.Fatalf("candidate=%#v err=%v", candidate, err)
		}
	})
	t.Run("non nil data and successful field authorization", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ValidateFields = func(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any, principalmodel.Principal) error {
			return nil
		}
		_, candidate, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", map[string]any{"name": "Acme"}, "", principal)
		if err != nil || candidate.Data["name"] != "Acme" {
			t.Fatalf("candidate=%#v err=%v", candidate, err)
		}
	})
	t.Run("planning dependency", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ValidatePipeline = func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error {
			return failure
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("replay rejected", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.FindReplay = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
			return recordmodel.Record{ID: "existing"}, true, nil
		}
		_, candidate, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if candidate.ID != "existing" || apperror.CodeOf(err) != "backend.record.create_replayed" {
			t.Fatalf("candidate=%#v err=%v", candidate, err)
		}
	})
	t.Run("candidate authorization error", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.CanWriteCandidate = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error) {
			return false, failure
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("field authorization error", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ValidateFields = func(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any, principalmodel.Principal) error {
			return failure
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if apperror.CodeOf(err) == "" || !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("canonical planner error", func(t *testing.T) {
		dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
		dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer", Config: map[string]any{"write_policy": "action_only"}}, nil
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(t.Context(), "customer", nil, "", principal)
		if apperror.CodeOf(err) == "" {
			t.Fatalf("planner error=%v", err)
		}
	})
}

func TestRecordCreateCommitErrorMapsBusinessConflict(t *testing.T) {
	err := recordCreateCommitError(mutation.BusinessConflict("backend.order.status_conflict", "order", "order-1", "status"))
	assertRecordCreateApplicationError(t, err, apperror.KindConflict, "backend.order.status_conflict", map[string]string{
		"object": "order", "record_id": "order-1", "policy": "status",
	})
}
