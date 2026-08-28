package service

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type pipelineEdgeRepository struct {
	pipelines []recordmodel.Record
	stages    []recordmodel.Record
	getErr    error
	listErr   error
}

func (r *pipelineEdgeRepository) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	if r.getErr != nil {
		return recordmodel.Record{}, false, r.getErr
	}
	records := r.pipelines
	if object.Key == "pipeline_stage" {
		records = r.stages
	}
	for _, record := range records {
		if record.ID == recordID {
			return record, true, nil
		}
	}
	return recordmodel.Record{}, false, nil
}

func (r *pipelineEdgeRepository) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	items := []recordmodel.Record{}
	for _, record := range r.stages {
		if object.Key == "pipeline_stage" && record.Data["pipeline"] == query.Filters["pipeline"] {
			items = append(items, record)
		}
	}
	return recordmodel.RecordPageResult{Items: items, Page: query.Page, PageSize: query.PageSize, Total: len(items)}, nil
}

func TestPipelineGetStageAndDefaultValidationEdges(t *testing.T) {
	repository := &pipelineEdgeRepository{stages: []recordmodel.Record{{ID: "stage-1", Data: map[string]any{"pipeline": "pipeline-1"}}, {ID: "denied", Data: map[string]any{"pipeline": "pipeline-1"}}}}
	service := pipelineEdgeService(repository)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	stage, err := service.GetStage(t.Context(), principal.WorkspaceID, " stage-1 ")
	if err != nil || stage.ID != "stage-1" {
		t.Fatalf("stage=%+v err=%v", stage, err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "other"}, "", nil, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "", map[string]any{"default_stage": nil}, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "", map[string]any{"default_stage": ""}, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "", map[string]any{"default_stage": "stage-1"}, principal); errorCode(err) != "backend.pipeline.default_stage_requires_existing_pipeline" {
		t.Fatalf("missing pipeline id err=%v", err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "pipeline-1", map[string]any{"default_stage": "denied"}, principal); errorCode(err) != "backend.record.outside_scope" {
		t.Fatalf("denied stage err=%v", err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "pipeline-2", map[string]any{"default_stage": "stage-1"}, principal); errorCode(err) != "backend.pipeline.stage_pipeline_mismatch" {
		t.Fatalf("mismatch err=%v", err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "pipeline-1", map[string]any{"default_stage": "stage-1"}, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "pipeline-1", map[string]any{"default_stage": "missing"}, principal); errorCode(err) != "backend.pipeline.stage_not_found" {
		t.Fatalf("missing default stage err=%v", err)
	}

	missingObject := NewPipelineDomainService(PipelineDependencies{Repository: repository})
	if _, err := missingObject.GetStage(t.Context(), "workspace-a", "stage-1"); errorCode(err) != "backend.pipeline.stage_object_missing" {
		t.Fatalf("missing stage object err=%v", err)
	}
	repository.getErr = errors.New("database unavailable")
	if _, err := service.GetStage(t.Context(), "workspace-a", "stage-1"); errorCode(err) != "backend.internal" || !errors.Is(err, repository.getErr) || apperror.ParamsOf(err)["operation"] != "get pipeline stage" {
		t.Fatalf("get failure err=%v", err)
	}
	repository.getErr = nil
	if _, err := service.GetStage(t.Context(), "workspace-a", "missing"); errorCode(err) != "backend.pipeline.stage_not_found" {
		t.Fatalf("missing stage err=%v", err)
	}
}

func TestPipelineApplyItemDefaultsCoversStageAndPipelinePaths(t *testing.T) {
	repository := &pipelineEdgeRepository{
		pipelines: []recordmodel.Record{
			{ID: "pipeline-default", Data: map[string]any{"default_stage": "stage-default"}},
			{ID: "pipeline-first", Data: map[string]any{}},
			{ID: "pipeline-blank-default", Data: map[string]any{"default_stage": ""}},
			{ID: "pipeline-empty", Data: map[string]any{}},
			{ID: "denied-pipeline", Data: map[string]any{"default_stage": "stage-default"}},
			{ID: "pipeline-denied-stage", Data: map[string]any{"default_stage": "denied"}},
			{ID: "pipeline-mismatch", Data: map[string]any{"default_stage": "stage-default"}},
		},
		stages: []recordmodel.Record{
			{ID: "stage-default", Data: map[string]any{"pipeline": "pipeline-default", "sort_order": 10, "sla_hours": 2}},
			{ID: "stage-first", Data: map[string]any{"pipeline": "pipeline-first", "sort_order": 1, "sla_hours": 1}},
			{ID: "stage-blank-default", Data: map[string]any{"pipeline": "pipeline-blank-default", "sort_order": 1}},
			{ID: "stage-later", Data: map[string]any{"pipeline": "pipeline-first", "sort_order": 2}},
			{ID: "stage-last", Data: map[string]any{"pipeline": "pipeline-first", "sort_order": 3}},
			{ID: "stage-invalid", Data: map[string]any{"pipeline": "pipeline-first", "sort_order": "invalid"}},
			{ID: "denied", Data: map[string]any{"pipeline": "pipeline-denied-stage"}},
		},
	}
	service := pipelineEdgeService(repository)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	item := definitionmodel.ObjectSchema{Key: "pipeline_item"}
	if err := service.ApplyItemDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "other"}, nil, principal, false); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{}, principal, false); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "", "current_stage": ""}, principal, false); err != nil {
		t.Fatal(err)
	}

	fromStage := map[string]any{"current_stage": "stage-default", "last_transition_at": "2026-07-19T10:00:00Z"}
	if err := service.ApplyItemDefaults(t.Context(), item, fromStage, principal, false); err != nil || fromStage["pipeline"] != "pipeline-default" || fromStage["due_at"] != "2026-07-19T12:00:00Z" {
		t.Fatalf("fromStage=%#v err=%v", fromStage, err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "other", "current_stage": "stage-default"}, principal, false); errorCode(err) != "backend.pipeline.stage_pipeline_mismatch" {
		t.Fatalf("stage mismatch err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"current_stage": "denied"}, principal, false); errorCode(err) != "backend.record.outside_scope" {
		t.Fatalf("denied stage err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"current_stage": "missing"}, principal, false); errorCode(err) != "backend.pipeline.stage_not_found" {
		t.Fatalf("missing current stage err=%v", err)
	}
	fromStageWithBlankPipeline := map[string]any{"pipeline": "", "current_stage": "stage-default"}
	if err := service.ApplyItemDefaults(t.Context(), item, fromStageWithBlankPipeline, principal, false); err != nil || fromStageWithBlankPipeline["pipeline"] != "pipeline-default" {
		t.Fatalf("blank pipeline from stage=%#v err=%v", fromStageWithBlankPipeline, err)
	}
	withCurrent := map[string]any{"pipeline": "pipeline-default", "current_stage": "stage-default"}
	if err := service.ApplyItemDefaults(t.Context(), item, withCurrent, principal, false); err != nil || withCurrent["due_at"] != "2026-07-19T14:00:00Z" {
		t.Fatalf("withCurrent=%#v err=%v", withCurrent, err)
	}

	withDefault := map[string]any{"pipeline": "pipeline-default"}
	if err := service.ApplyItemDefaults(t.Context(), item, withDefault, principal, false); err != nil || withDefault["current_stage"] != "stage-default" || withDefault["due_at"] != "2026-07-19T14:00:00Z" {
		t.Fatalf("withDefault=%#v err=%v", withDefault, err)
	}
	withFirst := map[string]any{"pipeline": "pipeline-first"}
	if err := service.ApplyItemDefaults(t.Context(), item, withFirst, principal, false); err != nil || withFirst["current_stage"] != "stage-first" || withFirst["due_at"] != "2026-07-19T13:00:00Z" {
		t.Fatalf("withFirst=%#v err=%v", withFirst, err)
	}
	withBlankDefault := map[string]any{"pipeline": "pipeline-blank-default"}
	if err := service.ApplyItemDefaults(t.Context(), item, withBlankDefault, principal, false); err != nil || withBlankDefault["current_stage"] != "stage-blank-default" {
		t.Fatalf("blank default pipeline=%#v err=%v", withBlankDefault, err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-empty"}, principal, false); errorCode(err) != "backend.pipeline.default_stage_required" {
		t.Fatalf("empty pipeline err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "missing"}, principal, false); errorCode(err) != "backend.pipeline.not_found" {
		t.Fatalf("missing pipeline err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "denied-pipeline"}, principal, false); errorCode(err) != "backend.record.outside_scope" {
		t.Fatalf("denied pipeline err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-denied-stage"}, principal, false); errorCode(err) != "backend.record.outside_scope" {
		t.Fatalf("denied default stage err=%v", err)
	}
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-mismatch"}, principal, false); errorCode(err) != "backend.pipeline.stage_pipeline_mismatch" {
		t.Fatalf("default stage mismatch err=%v", err)
	}
	next, err := service.NextStage(t.Context(), "workspace-a", "pipeline-first", recordmodel.Record{ID: "current", Data: map[string]any{"sort_order": 1.5}})
	if err != nil || next.ID != "stage-later" {
		t.Fatalf("next=%+v err=%v", next, err)
	}

	repository.getErr = errors.New("read failed")
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-default"}, principal, false); errorCode(err) != "backend.internal" || !errors.Is(err, repository.getErr) {
		t.Fatalf("pipeline read failure err=%v", err)
	}
	repository.getErr = nil
	repository.listErr = errors.New("list failed")
	if err := service.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-first"}, principal, false); errorCode(err) != "backend.internal" || !errors.Is(err, repository.listErr) {
		t.Fatalf("stage list failure err=%v", err)
	}
	if _, err := service.NextStage(t.Context(), "workspace-a", "pipeline-first", recordmodel.Record{}); errorCode(err) != "backend.internal" {
		t.Fatalf("next stage list failure err=%v", err)
	}

	missingObject := NewPipelineDomainService(PipelineDependencies{Repository: repository, Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		return definitionmodel.ObjectSchema{}, false
	}})
	if err := missingObject.ApplyItemDefaults(t.Context(), item, map[string]any{"pipeline": "pipeline-default"}, principal, false); errorCode(err) != "backend.pipeline.object_missing" {
		t.Fatalf("missing pipeline object err=%v", err)
	}
	if _, err := missingObject.FirstStage(t.Context(), "workspace-a", "pipeline-default"); errorCode(err) != "backend.pipeline.stage_object_missing" {
		t.Fatalf("missing stage object err=%v", err)
	}
}

func TestPipelinePermissionSLAAndValueEdges(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	service := NewPipelineDomainService(PipelineDependencies{Now: func() time.Time { return now }})
	stage := recordmodel.Record{Data: map[string]any{"advance_permission": "pipeline.advance"}}
	if err := service.ValidateStagePermission(stage, principalmodel.Principal{}); errorCode(err) != "backend.pipeline.stage_permission_denied" {
		t.Fatalf("permission err=%v", err)
	}
	if err := service.ValidateStagePermission(stage, accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"pipeline.advance"}})); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateStagePermission(recordmodel.Record{Data: map[string]any{}}, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateStagePermission(recordmodel.Record{Data: map[string]any{"advance_permission": ""}}, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStageBelongsToPipeline(recordmodel.Record{Data: map[string]any{"pipeline": "other"}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStageBelongsToPipeline(recordmodel.Record{Data: map[string]any{}}, "pipeline-1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequiredFields(recordmodel.Record{Data: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequiredFields(recordmodel.Record{Data: map[string]any{"required_fields": "name"}}, map[string]any{"name": "value"}); err != nil {
		t.Fatal(err)
	}

	data := map[string]any{"due_at": "existing"}
	if err := service.ApplyStageSLA(data, recordmodel.Record{Data: map[string]any{"sla_hours": 0}}, "", true); err != nil || data["due_at"] != "" {
		t.Fatalf("cleared SLA=%#v err=%v", data, err)
	}
	data = map[string]any{}
	if err := service.ApplyStageSLA(data, recordmodel.Record{Data: map[string]any{"sla_hours": int64(3)}}, "invalid", false); err != nil || data["due_at"] != "2026-07-19T15:00:00Z" {
		t.Fatalf("clock SLA=%#v err=%v", data, err)
	}
	data = map[string]any{"due_at": ""}
	if err := service.ApplyStageSLA(data, recordmodel.Record{Data: map[string]any{"sla_hours": "invalid"}}, "", false); err != nil || data["due_at"] != "" {
		t.Fatalf("blank due/invalid SLA=%#v err=%v", data, err)
	}
	if values := splitCSV(" , value "); len(values) != 1 || values[0] != "value" {
		t.Fatalf("split values=%#v", values)
	}
	if err := pipelineError(apperror.KindBadRequest, "backend.pipeline.test", nil, " ", "ignored"); apperror.ParamsOf(err) != nil {
		t.Fatalf("blank error params=%#v", apperror.ParamsOf(err))
	}
	for _, test := range []struct {
		value any
		want  float64
		ok    bool
	}{{1, 1, true}, {int64(2), 2, true}, {3.5, 3.5, true}, {" 4.5 ", 4.5, true}, {"bad", 0, false}, {nil, 0, false}} {
		got, ok := floatValue(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("floatValue(%#v)=(%v,%v)", test.value, got, ok)
		}
	}
}

func pipelineEdgeService(repository *pipelineEdgeRepository) *PipelineDomainService {
	return NewPipelineDomainService(PipelineDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: key}, key == "pipeline" || key == "pipeline_stage"
		},
		CanAccess: func(_ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
			return record.ID != "denied" && record.ID != "denied-pipeline"
		},
		Now: func() time.Time { return time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC) },
	})
}
