package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	pipelinecontract "github.com/domainry/domainry-runtime/runtime/domain/pipeline/contract"
)

type PipelineDependencies struct {
	Repository pipelinecontract.PipelineRecordReader
	Object     func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	CanAccess  func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	Now        func() time.Time
}

// PipelineDomainService coordinates mutation pipelines.
type PipelineDomainService struct {
	dependencies PipelineDependencies
}

func NewPipelineDomainService(dependencies PipelineDependencies) *PipelineDomainService {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &PipelineDomainService{dependencies: dependencies}
}

func (s *PipelineDomainService) GetStage(ctx context.Context, workspaceID, stageID string) (recordmodel.Record, error) {
	stageObject, ok := s.object(ctx, "pipeline_stage")
	if !ok {
		return recordmodel.Record{}, pipelineError(apperror.KindBadRequest, "backend.pipeline.stage_object_missing", nil)
	}
	stage, found, err := s.dependencies.Repository.GetRecord(ctx, workspaceID, stageObject, strings.TrimSpace(stageID))
	if err != nil {
		return recordmodel.Record{}, pipelineError(apperror.KindInternal, "backend.internal", err, "operation", "get pipeline stage")
	}
	if !found {
		return recordmodel.Record{}, pipelineError(apperror.KindBadRequest, "backend.pipeline.stage_not_found", nil)
	}
	return stage, nil
}

func (s *PipelineDomainService) ValidateDefaults(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal) error {
	if object.Key != "pipeline" {
		return nil
	}
	defaultStageID := strings.TrimSpace(fmt.Sprint(data["default_stage"]))
	if defaultStageID == "" || defaultStageID == "<nil>" {
		return nil
	}
	if strings.TrimSpace(recordID) == "" {
		return pipelineError(apperror.KindBadRequest, "backend.pipeline.default_stage_requires_existing_pipeline", nil)
	}
	stage, err := s.GetStage(ctx, principal.WorkspaceID, defaultStageID)
	if err != nil {
		return err
	}
	stageObject, _ := s.object(ctx, "pipeline_stage")
	if !s.canAccess(principal, stageObject, stage) {
		return pipelineError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	return ValidateStageBelongsToPipeline(stage, recordID)
}

func (s *PipelineDomainService) ApplyItemDefaults(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal, overwriteSLA bool) error {
	if object.Key != "pipeline_item" {
		return nil
	}
	pipelineID := strings.TrimSpace(fmt.Sprint(data["pipeline"]))
	stageID := strings.TrimSpace(fmt.Sprint(data["current_stage"]))
	if stageID != "" && stageID != "<nil>" {
		stage, err := s.GetStage(ctx, principal.WorkspaceID, stageID)
		if err != nil {
			return err
		}
		stageObject, _ := s.object(ctx, "pipeline_stage")
		if !s.canAccess(principal, stageObject, stage) {
			return pipelineError(apperror.KindForbidden, "backend.record.outside_scope", nil)
		}
		transitionAt := strings.TrimSpace(fmt.Sprint(data["last_transition_at"]))
		stagePipelineID := strings.TrimSpace(fmt.Sprint(stage.Data["pipeline"]))
		if pipelineID == "" || pipelineID == "<nil>" {
			data["pipeline"] = stagePipelineID
			return s.ApplyStageSLA(data, stage, transitionAt, overwriteSLA)
		}
		if err := ValidateStageBelongsToPipeline(stage, pipelineID); err != nil {
			return err
		}
		return s.ApplyStageSLA(data, stage, transitionAt, overwriteSLA)
	}
	if pipelineID == "" || pipelineID == "<nil>" {
		return nil
	}
	pipelineObject, ok := s.object(ctx, "pipeline")
	if !ok {
		return pipelineError(apperror.KindBadRequest, "backend.pipeline.object_missing", nil)
	}
	pipeline, found, err := s.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, pipelineObject, pipelineID)
	if err != nil {
		return pipelineError(apperror.KindInternal, "backend.internal", err, "operation", "get pipeline")
	}
	if !found {
		return pipelineError(apperror.KindBadRequest, "backend.pipeline.not_found", nil)
	}
	if !s.canAccess(principal, pipelineObject, pipeline) {
		return pipelineError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	defaultStageID := strings.TrimSpace(fmt.Sprint(pipeline.Data["default_stage"]))
	var stage recordmodel.Record
	if defaultStageID != "" && defaultStageID != "<nil>" {
		stage, err = s.GetStage(ctx, principal.WorkspaceID, defaultStageID)
	} else {
		stage, err = s.FirstStage(ctx, principal.WorkspaceID, pipelineID)
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(stage.ID) == "" {
		return pipelineError(apperror.KindBadRequest, "backend.pipeline.default_stage_required", nil)
	}
	stageObject, _ := s.object(ctx, "pipeline_stage")
	if !s.canAccess(principal, stageObject, stage) {
		return pipelineError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	if err := ValidateStageBelongsToPipeline(stage, pipelineID); err != nil {
		return err
	}
	data["current_stage"] = stage.ID
	return s.ApplyStageSLA(data, stage, strings.TrimSpace(fmt.Sprint(data["last_transition_at"])), overwriteSLA)
}

func (s *PipelineDomainService) ApplyStageSLA(data map[string]any, stage recordmodel.Record, transitionAt string, overwrite bool) error {
	if dueAt := strings.TrimSpace(fmt.Sprint(data["due_at"])); dueAt != "" && dueAt != "<nil>" && !overwrite {
		return nil
	}
	slaHours, ok := floatValue(stage.Data["sla_hours"])
	if !ok || slaHours <= 0 {
		if overwrite {
			data["due_at"] = ""
		}
		return nil
	}
	base := s.dependencies.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(transitionAt)); err == nil {
		base = parsed.UTC()
	}
	data["due_at"] = base.Add(time.Duration(slaHours * float64(time.Hour))).Format(time.RFC3339)
	return nil
}

func (s *PipelineDomainService) NextStage(ctx context.Context, workspaceID, pipelineID string, current recordmodel.Record) (recordmodel.Record, error) {
	stages, err := s.stages(ctx, workspaceID, pipelineID)
	if err != nil {
		return recordmodel.Record{}, err
	}
	currentOrder, _ := floatValue(current.Data["sort_order"])
	var next recordmodel.Record
	nextOrder := 0.0
	for _, stage := range stages {
		if strings.TrimSpace(stage.ID) == strings.TrimSpace(current.ID) {
			continue
		}
		order, ok := floatValue(stage.Data["sort_order"])
		if !ok || order <= currentOrder {
			continue
		}
		if next.ID == "" || order < nextOrder {
			next, nextOrder = stage, order
		}
	}
	return next, nil
}

func (s *PipelineDomainService) FirstStage(ctx context.Context, workspaceID, pipelineID string) (recordmodel.Record, error) {
	stages, err := s.stages(ctx, workspaceID, pipelineID)
	if err != nil {
		return recordmodel.Record{}, err
	}
	var first recordmodel.Record
	firstOrder := 0.0
	for _, stage := range stages {
		order, ok := floatValue(stage.Data["sort_order"])
		if ok && (first.ID == "" || order < firstOrder) {
			first, firstOrder = stage, order
		}
	}
	return first, nil
}

func (s *PipelineDomainService) ValidateStagePermission(stage recordmodel.Record, principal principalmodel.Principal) error {
	permission := strings.TrimSpace(fmt.Sprint(stage.Data["advance_permission"]))
	if permission != "" && permission != "<nil>" && !principal.HasPermission(permission) {
		return pipelineError(apperror.KindForbidden, "backend.pipeline.stage_permission_denied", nil)
	}
	return nil
}

func ValidateStageBelongsToPipeline(stage recordmodel.Record, pipelineID string) error {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return nil
	}
	stagePipelineID := pipelineStringValue(stage.Data["pipeline"])
	if stagePipelineID != "" && stagePipelineID != pipelineID {
		return pipelineError(apperror.KindBadRequest, "backend.pipeline.stage_pipeline_mismatch", nil)
	}
	return nil
}

func pipelineStringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func ValidateRequiredFields(stage recordmodel.Record, data map[string]any) error {
	for _, fieldKey := range splitCSV(fmt.Sprint(stage.Data["required_fields"])) {
		if recordcontract.RecordIsEmptyValue(data[fieldKey]) {
			return pipelineError(apperror.KindBadRequest, "backend.pipeline.required_field_missing", nil, "field", fieldKey)
		}
	}
	return nil
}

func (s *PipelineDomainService) stages(ctx context.Context, workspaceID, pipelineID string) ([]recordmodel.Record, error) {
	stageObject, ok := s.object(ctx, "pipeline_stage")
	if !ok {
		return nil, pipelineError(apperror.KindBadRequest, "backend.pipeline.stage_object_missing", nil)
	}
	page, err := s.dependencies.Repository.ListRecords(ctx, workspaceID, stageObject, recordmodel.RecordListQuery{Page: 1, PageSize: 500, Filters: map[string]any{"pipeline": pipelineID}})
	if err != nil {
		return nil, pipelineError(apperror.KindInternal, "backend.internal", err, "operation", "list pipeline stages")
	}
	return page.Items, nil
}

func (s *PipelineDomainService) object(ctx context.Context, key string) (definitionmodel.ObjectSchema, bool) {
	if s.dependencies.Object == nil {
		return definitionmodel.ObjectSchema{}, false
	}
	return s.dependencies.Object(ctx, key)
}

func (s *PipelineDomainService) canAccess(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return s.dependencies.CanAccess == nil || s.dependencies.CanAccess(principal, object, record)
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(strings.TrimSpace(value), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" && part != "<nil>" {
			out = append(out, part)
		}
	}
	return out
}

func pipelineError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}
