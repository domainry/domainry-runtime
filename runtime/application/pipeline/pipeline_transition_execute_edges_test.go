package pipeline

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type pipelineCodedTestError struct{}

func (pipelineCodedTestError) Error() string     { return "coded" }
func (pipelineCodedTestError) ErrorCode() string { return "pipeline.coded" }
func (pipelineCodedTestError) ErrorParams() map[string]string {
	return map[string]string{"field": "stage"}
}

func pipelineExecuteFixture(t *testing.T) (*PipelineTransitionApplicationService, definitionmodel.ObjectSchema, recordmodel.Record) {
	return pipelineExecuteFixtureWithCommit(t, nil)
}

func pipelineExecuteFixtureWithCommit(t *testing.T, commitErr error) (*PipelineTransitionApplicationService, definitionmodel.ObjectSchema, recordmodel.Record) {
	t.Helper()
	service, object, record, _ := pipelineTransitionFixture(t, commitErr)
	service.dependencies.ActionAllowed = func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return true }
	service.dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return object, nil
	}
	service.dependencies.GetRecord = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return record, true, nil
	}
	service.dependencies.CanAccess = func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true }
	return service, object, record
}

func TestPipelineTransitionExecuteBoundariesAndHappyPaths(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}, accessfixture.Bundle{
		Permissions: []string{"pipeline_item.read", "pipeline_item.update", "pipeline_item.advance", "pipeline_item.reopen"},
		RecordScope: "all_records",
	})
	action := definitionmodel.ActionSchema{Key: "pipeline_item.advance", ObjectKey: "pipeline_item"}
	service, object, record := pipelineExecuteFixture(t)
	if !service.IsAction(action) || service.IsAction(definitionmodel.ActionSchema{Key: "other"}) {
		t.Fatal("pipeline action classification mismatch")
	}
	result, err := service.Execute(t.Context(), object.Key, record.ID, action, map[string]any{"to_stage": "stage-new"}, principal)
	if err != nil || result.RecordID != record.ID {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	reopenService, _, _ := pipelineExecuteFixture(t)
	result, err = reopenService.Execute(t.Context(), object.Key, record.ID, definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}, map[string]any{"to_stage": "stage-new"}, principal)
	if err != nil || result.Record.Data["current_stage"] != "stage-new" {
		t.Fatalf("reopen=%+v err=%v", result, err)
	}

	tests := []struct {
		name string
		edit func(*PipelineTransitionApplicationService)
		data map[string]any
		code string
	}{
		{"permission", func(s *PipelineTransitionApplicationService) {
			s.dependencies.ActionAllowed = func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return false }
		}, nil, "backend.action.permission_denied"},
		{"object", func(s *PipelineTransitionApplicationService) {
			s.dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				return definitionmodel.ObjectSchema{}, errors.New("denied")
			}
		}, nil, ""},
		{"get failure", func(s *PipelineTransitionApplicationService) {
			s.dependencies.GetRecord = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
				return recordmodel.Record{}, false, errors.New("read")
			}
		}, nil, "backend.internal"},
		{"not found", func(s *PipelineTransitionApplicationService) {
			s.dependencies.GetRecord = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
				return recordmodel.Record{}, false, nil
			}
		}, nil, "backend.record.not_found"},
		{"scope", func(s *PipelineTransitionApplicationService) {
			s.dependencies.CanAccess = func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false }
		}, nil, "backend.record.outside_scope"},
		{"precondition", func(s *PipelineTransitionApplicationService) {
			s.dependencies.CheckPrecondition = func(context.Context, definitionmodel.ActionSchema, recordmodel.Record, principalmodel.Principal) error {
				return errors.New("precondition")
			}
		}, nil, "backend.bad_request"},
		{"expected version", func(*PipelineTransitionApplicationService) {}, map[string]any{"expected_version": 99}, "backend.pipeline.version_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, _, _ := pipelineExecuteFixture(t)
			test.edit(candidate)
			_, err := candidate.Execute(t.Context(), object.Key, record.ID, action, test.data, principal)
			if err == nil || (test.code != "" && apperror.CodeOf(err) != test.code) {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}

	missingCurrent, _, _ := pipelineExecuteFixture(t)
	missingCurrent.dependencies.GetRecord = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: "item", Data: map[string]any{}}, true, nil
	}
	if _, err := missingCurrent.Execute(t.Context(), object.Key, "item", action, nil, principal); apperror.CodeOf(err) != "backend.pipeline.current_stage_required" {
		t.Fatalf("missing current stage error=%v", err)
	}
}

func TestPipelineTransitionHelpersAndNilOptionalPorts(t *testing.T) {
	if pipelineTransitionClean(nil) != "" || pipelineTransitionClean(" value ") != "value" || pipelineTransitionValueOr(" ", "fallback") != "fallback" {
		t.Fatal("transition string normalization mismatch")
	}
	appErr := &apperror.AppError{Kind: apperror.KindConflict, Code: "conflict"}
	if pipelineTransitionBadRequest(appErr) != appErr {
		t.Fatal("application error was wrapped")
	}
	if err := pipelineTransitionBadRequest(pipelineCodedTestError{}); apperror.CodeOf(err) != "pipeline.coded" {
		t.Fatalf("coded error=%v", err)
	}
	service := NewPipelineTransitionApplicationService(PipelineTransitionDependencies{})
	if service.dependencies.Now == nil {
		t.Fatal("default clock missing")
	}
	service.audit(t.Context(), "event", "object", "record", principalmodel.Principal{}, "summary", nil, nil)
	var nilService *PipelineTransitionApplicationService
	nilService.audit(t.Context(), "event", "object", "record", principalmodel.Principal{}, "summary", nil, nil)
}

func TestPipelineTransitionDestinationAndReopenEdges(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}}
	service, object, record := pipelineExecuteFixture(t)
	stage, err := service.destinationStage(t.Context(), principal.WorkspaceID, "", "stage-old", map[string]any{"to_stage": "stage-new"})
	if err != nil || stage.ID != "stage-new" {
		t.Fatalf("derived destination=%+v err=%v", stage, err)
	}
	stage, err = service.destinationStage(t.Context(), principal.WorkspaceID, "", "stage-old", nil)
	if err != nil || stage.ID != "stage-new" {
		t.Fatalf("next destination=%+v err=%v", stage, err)
	}
	if _, err := service.destinationStage(t.Context(), principal.WorkspaceID, "pipeline-1", "missing", nil); err == nil {
		t.Fatal("missing current stage unexpectedly succeeded")
	}
	if _, err := service.destinationStage(t.Context(), principal.WorkspaceID, "pipeline-1", "stage-old", map[string]any{"to_stage": "missing"}); err == nil {
		t.Fatal("missing target stage unexpectedly succeeded")
	}
	if _, err := service.advance(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.advance"}, map[string]any{"to_stage": "stage-old"}, principal); apperror.CodeOf(err) != "backend.pipeline.same_stage" {
		t.Fatalf("same-stage advance error=%v", err)
	}
	if _, err := service.reopen(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}, map[string]any{"to_stage": "stage-old"}, principal); apperror.CodeOf(err) != "backend.pipeline.same_stage" {
		t.Fatalf("same-stage reopen error=%v", err)
	}
	if _, err := service.reopen(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}, nil, principal); apperror.CodeOf(err) != "backend.pipeline.same_stage" {
		t.Fatalf("default-stage reopen error=%v", err)
	}
	if _, err := service.reopen(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}, map[string]any{"to_stage": "missing"}, principal); err == nil {
		t.Fatal("missing reopen target unexpectedly succeeded")
	}
}

func TestPipelineTransitionDirectCommitFailures(t *testing.T) {
	wantErr := errors.New("commit unavailable")
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}
	service, object, record := pipelineExecuteFixtureWithCommit(t, wantErr)
	action := definitionmodel.ActionSchema{Key: "pipeline_item.advance", ObjectKey: object.Key}
	if _, err := service.Execute(t.Context(), object.Key, record.ID, action, map[string]any{"to_stage": "stage-new"}, principal); !errors.Is(err, wantErr) {
		t.Fatalf("execute commit error=%v", err)
	}
	service, object, record = pipelineExecuteFixtureWithCommit(t, wantErr)
	if _, err := service.advance(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); !errors.Is(err, wantErr) {
		t.Fatalf("advance commit error=%v", err)
	}
	service, object, record = pipelineExecuteFixtureWithCommit(t, wantErr)
	if _, err := service.reopen(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}, map[string]any{"to_stage": "stage-new"}, principal); !errors.Is(err, wantErr) {
		t.Fatalf("reopen commit error=%v", err)
	}
}
