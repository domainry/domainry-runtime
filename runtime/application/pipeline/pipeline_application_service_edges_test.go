package pipeline

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestPipelineApplicationServiceDelegatesRulesAndNilBoundaries(t *testing.T) {
	var nilService *PipelineApplicationService
	if _, ok := nilService.pipelineObject(t.Context(), "missing"); ok {
		t.Fatal("nil service returned object")
	}
	if dependencies := nilService.domainDependencies(); dependencies.Repository != nil || dependencies.Object != nil {
		t.Fatalf("nil dependencies=%+v", dependencies)
	}
	transition, object, _, _ := pipelineTransitionFixture(t, nil)
	service := transition.dependencies.Pipeline
	if _, ok := service.pipelineObject(t.Context(), object.Key); !ok {
		t.Fatal("fixture object not found")
	}
	if _, ok := NewPipelineApplicationService(PipelineDependencies{}).pipelineObject(t.Context(), "missing"); ok {
		t.Fatal("missing object callback returned object")
	}
	if dependencies := service.domainDependencies(); dependencies.Repository == nil || dependencies.Object == nil || dependencies.Now == nil {
		t.Fatalf("dependencies=%+v", dependencies)
	}
	stage, err := service.GetStage(t.Context(), "workspace-1", "stage-old")
	if err != nil || stage.ID != "stage-old" {
		t.Fatalf("stage=%+v err=%v", stage, err)
	}
	first, err := service.FirstStage(t.Context(), "workspace-1", "pipeline-1")
	if err != nil || first.ID != "stage-old" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	next, err := service.NextStage(t.Context(), "workspace-1", "pipeline-1", first)
	if err != nil || next.ID != "stage-new" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}
	data := map[string]any{}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{}, "", data, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyItemDefaults(t.Context(), definitionmodel.ObjectSchema{}, data, principal, false); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateStagePermission(recordmodel.Record{}, principal); err != nil {
		t.Fatal(err)
	}
	if NewPipelineApplicationService(PipelineDependencies{}).dependencies.Now == nil {
		t.Fatal("default clock not installed")
	}
}
