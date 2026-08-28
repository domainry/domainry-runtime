package action

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestActionPreconditionApplicationChecksGenericAndPipelineRules(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	service := NewActionPreconditionApplicationService(ActionPreconditionDependencies{})
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"amount > 0"}}, recordmodel.Record{Data: map[string]any{"amount": 0}}, principal); apperror.CodeOf(err) != "backend.action.precondition_failed" {
		t.Fatalf("generic error=%v", err)
	}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, recordmodel.Record{}, principal); apperror.CodeOf(err) != "backend.action.precondition_failed" {
		t.Fatalf("missing stage error=%v", err)
	}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, recordmodel.Record{Data: map[string]any{"current_stage": "stage-1"}}, principal); apperror.CodeOf(err) != "backend.action.precondition_failed" {
		t.Fatalf("missing pipeline stage port error=%v", err)
	}

	service.dependencies.PipelineStage = func(context.Context, string, string) (recordmodel.Record, error) {
		return recordmodel.Record{}, errActionPreconditionStage
	}
	record := recordmodel.Record{Data: map[string]any{"current_stage": "stage-1"}}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, record, principal); !errors.Is(err, errActionPreconditionStage) {
		t.Fatalf("stage lookup error=%v", err)
	}
	service.dependencies.PipelineStage = func(context.Context, string, string) (recordmodel.Record, error) {
		return recordmodel.Record{Data: map[string]any{"stage_type": "active"}}, nil
	}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, record, principal); apperror.CodeOf(err) != "backend.action.precondition_failed" {
		t.Fatalf("active stage error=%v", err)
	}
	service.dependencies.PipelineStage = func(context.Context, string, string) (recordmodel.Record, error) {
		return recordmodel.Record{Data: map[string]any{"is_terminal": true}}, nil
	}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, record, principal); err != nil {
		t.Fatalf("terminal stage error=%v", err)
	}
	if err := service.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"unrecognized runtime constant"}}, record, principal); err != nil {
		t.Fatalf("unknown non-expression should remain an authoring concern: %v", err)
	}
}

func TestPreconditionPipelineStageTerminal(t *testing.T) {
	for _, stageType := range []string{"won", "lost", "done", "cancelled", "failed"} {
		if !preconditionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"stage_type": stageType}}) {
			t.Fatalf("stage %s should be terminal", stageType)
		}
	}
	if preconditionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"stage_type": "active"}}) {
		t.Fatal("active stage should not be terminal")
	}
	if preconditionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"is_terminal": false, "stage_type": "active"}}) {
		t.Fatal("explicit false terminal flag accepted")
	}
}

var errActionPreconditionStage = errors.New("pipeline stage lookup failed")
