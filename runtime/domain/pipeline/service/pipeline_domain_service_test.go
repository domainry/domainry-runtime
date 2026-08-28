// Pipeline domain service tests.
package service

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type pipelineRepositoryProbe struct {
	stages []recordmodel.Record
}

func (r *pipelineRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Items: append([]recordmodel.Record(nil), r.stages...), Page: query.Page, PageSize: query.PageSize, Total: len(r.stages)}, nil
}

func (r *pipelineRepositoryProbe) GetRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	for _, stage := range r.stages {
		if stage.ID == recordID {
			return stage, true, nil
		}
	}
	return recordmodel.Record{}, false, nil
}

func TestServiceAppliesStageSLAWithFixedClock(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	service := NewPipelineDomainService(PipelineDependencies{Now: func() time.Time { return now }})
	data := map[string]any{}
	if err := service.ApplyStageSLA(data, recordmodel.Record{Data: map[string]any{"sla_hours": 2.5}}, "", false); err != nil {
		t.Fatal(err)
	}
	if got := data["due_at"]; got != "2026-07-17T12:30:00Z" {
		t.Fatalf("due_at=%v", got)
	}
	data["due_at"] = "kept"
	if err := service.ApplyStageSLA(data, recordmodel.Record{Data: map[string]any{"sla_hours": 4}}, "", false); err != nil || data["due_at"] != "kept" {
		t.Fatalf("non-overwrite data=%#v err=%v", data, err)
	}
}

func TestServiceSelectsOrderedPipelineStages(t *testing.T) {
	repository := &pipelineRepositoryProbe{stages: []recordmodel.Record{
		{ID: "stage-3", Data: map[string]any{"pipeline": "sales", "sort_order": 30}},
		{ID: "stage-1", Data: map[string]any{"pipeline": "sales", "sort_order": 10}},
		{ID: "stage-2", Data: map[string]any{"pipeline": "sales", "sort_order": 20}},
	}}
	service := NewPipelineDomainService(PipelineDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: key}, key == "pipeline_stage"
		},
	})
	first, err := service.FirstStage(t.Context(), "workspace-a", "sales")
	if err != nil || first.ID != "stage-1" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	next, err := service.NextStage(t.Context(), "workspace-a", "sales", first)
	if err != nil || next.ID != "stage-2" {
		t.Fatalf("next=%#v err=%v", next, err)
	}
}

func TestPipelineRulesReturnStructuredErrors(t *testing.T) {
	if err := ValidateStageBelongsToPipeline(recordmodel.Record{Data: map[string]any{"pipeline": "other"}}, "sales"); errorCode(err) != "backend.pipeline.stage_pipeline_mismatch" {
		t.Fatalf("pipeline mismatch err=%v", err)
	}
	if err := ValidateRequiredFields(recordmodel.Record{Data: map[string]any{"required_fields": "name, amount"}}, map[string]any{"name": "Deal"}); errorCode(err) != "backend.pipeline.required_field_missing" {
		t.Fatalf("required fields err=%v", err)
	}
}

func errorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}
