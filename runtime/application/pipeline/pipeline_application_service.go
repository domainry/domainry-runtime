package pipeline

import (
	"context"
	"time"

	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	pipelinedomain "github.com/domainry/domainry-runtime/runtime/domain/pipeline/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// PipelineDependencies is the complete owner boundary for Pipeline use cases.
// It deliberately exposes capabilities instead of a composition-root aggregate.
type PipelineDependencies struct {
	Repository recordrepository.RecordRepository
	Object     func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	CanAccess  func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	Now        func() time.Time
}

// PipelineApplicationService owns explicit Pipeline stage lookup, defaults and SLA behavior.
type PipelineApplicationService struct {
	dependencies PipelineDependencies
	rules        *pipelinedomain.PipelineDomainService
}

func NewPipelineApplicationService(dependencies PipelineDependencies) *PipelineApplicationService {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	service := &PipelineApplicationService{dependencies: dependencies}
	service.rules = pipelinedomain.NewPipelineDomainService(service.domainDependencies())
	return service
}

func (s *PipelineApplicationService) pipelineObject(ctx context.Context, key string) (definitionmodel.ObjectSchema, bool) {
	if s == nil || s.dependencies.Object == nil {
		return definitionmodel.ObjectSchema{}, false
	}
	return s.dependencies.Object(ctx, key)
}

func (s *PipelineApplicationService) domainDependencies() pipelinedomain.PipelineDependencies {
	if s == nil {
		return pipelinedomain.PipelineDependencies{}
	}
	return pipelinedomain.PipelineDependencies{
		Repository: s.dependencies.Repository,
		Object:     s.dependencies.Object,
		CanAccess:  s.dependencies.CanAccess,
		Now:        s.dependencies.Now,
	}
}

func (s *PipelineApplicationService) GetStage(ctx context.Context, workspaceID, stageID string) (recordmodel.Record, error) {
	return s.rules.GetStage(ctx, workspaceID, stageID)
}

func (s *PipelineApplicationService) ValidateDefaults(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal) error {
	return s.rules.ValidateDefaults(ctx, object, recordID, data, principal)
}

func (s *PipelineApplicationService) ApplyItemDefaults(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal, overwriteSLA bool) error {
	return s.rules.ApplyItemDefaults(ctx, object, data, principal, overwriteSLA)
}

func (s *PipelineApplicationService) ApplyStageSLA(data map[string]any, stage recordmodel.Record, transitionAt string, overwrite bool) error {
	return s.rules.ApplyStageSLA(data, stage, transitionAt, overwrite)
}

func (s *PipelineApplicationService) NextStage(ctx context.Context, workspaceID, pipelineID string, current recordmodel.Record) (recordmodel.Record, error) {
	return s.rules.NextStage(ctx, workspaceID, pipelineID, current)
}

func (s *PipelineApplicationService) FirstStage(ctx context.Context, workspaceID, pipelineID string) (recordmodel.Record, error) {
	return s.rules.FirstStage(ctx, workspaceID, pipelineID)
}

func (s *PipelineApplicationService) ValidateStagePermission(stage recordmodel.Record, principal principalmodel.Principal) error {
	return s.rules.ValidateStagePermission(stage, principal)
}
