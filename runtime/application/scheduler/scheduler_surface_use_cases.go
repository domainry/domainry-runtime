package scheduler

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulerauthoring "github.com/domainry/domainry-scheduler-sdk/authoring"
)

type TenantAdminSchedulerDefinitionDTO = schedulerauthoring.TenantAdminDefinition
type TenantAdminSchedulerDefinitionVersionDTO = schedulerauthoring.TenantAdminDefinitionVersion
type TenantAdminSchedulerAuthoringContract = schedulerauthoring.TenantAdminAuthoringContract

func (s *SchedulerApplicationService) TenantAdminDefinitions(ctx context.Context, principal principalmodel.Principal) ([]TenantAdminSchedulerDefinitionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionListTenantAdminSchedulerDefinitions); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, unavailableDefinitionSource()
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		return nil, internalError("list tenant admin scheduler definitions", err)
	}
	out := make([]TenantAdminSchedulerDefinitionDTO, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, projectTenantAdminSchedulerDefinition(definition))
	}
	return out, nil
}

func (s *SchedulerApplicationService) TenantAdminDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (TenantAdminSchedulerDefinitionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetTenantAdminSchedulerDefinition); err != nil {
		return TenantAdminSchedulerDefinitionDTO{}, err
	}
	definition, err := s.schedulerDefinition(ctx, definitionID)
	if err != nil {
		return TenantAdminSchedulerDefinitionDTO{}, err
	}
	return projectTenantAdminSchedulerDefinition(definition), nil
}

func (s *SchedulerApplicationService) TenantAdminDefinitionVersions(ctx context.Context, definitionID string, principal principalmodel.Principal) ([]TenantAdminSchedulerDefinitionVersionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetTenantAdminSchedulerDefinition); err != nil {
		return nil, err
	}
	if _, err := s.schedulerDefinition(ctx, definitionID); err != nil {
		return nil, err
	}
	versions, err := s.definitions.ListSchedulerDefinitionVersions(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	out := make([]TenantAdminSchedulerDefinitionVersionDTO, 0, len(versions))
	for _, version := range versions {
		out = append(out, TenantAdminSchedulerDefinitionVersionDTO{
			VersionID: version.VersionID,
			Event:     version.Event,
			CreatedAt: version.CreatedAt,
			Value:     projectTenantAdminSchedulerDefinition(PublishedDefinition{Key: definitionID, Data: version.Data, CreatedAt: version.CreatedAt}),
		})
	}
	return out, nil
}

func (s *SchedulerApplicationService) TenantAdminAuthoringContract(_ context.Context, principal principalmodel.Principal) (TenantAdminSchedulerAuthoringContract, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetTenantAdminSchedulerAuthoringContract); err != nil {
		return TenantAdminSchedulerAuthoringContract{}, err
	}
	return schedulerauthoring.TenantAdminContract(), nil
}

func (s *SchedulerApplicationService) AuthorizeOpsRead(ctx context.Context, principal principalmodel.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return schedulerExactQueryAllowed(principal, ActionGetOpsSchedulerState)
}

func projectTenantAdminSchedulerDefinition(definition PublishedDefinition) TenantAdminSchedulerDefinitionDTO {
	return schedulerauthoring.ProjectTenantAdminDefinition(schedulerauthoring.DefinitionProjection{Key: definition.Key, Data: definition.Data, CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt})
}

func unavailableDefinitionSource() error {
	return schedulerErrorUnavailable("backend.scheduler.definition_source_unavailable")
}

func schedulerErrorUnavailable(code string) error {
	return schedulerError(apperror.KindUnavailable, code, nil)
}
