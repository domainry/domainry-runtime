package scheduler

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulerauthoring "github.com/domainry/domainry-scheduler-sdk/authoring"
)

type ManagementSchedulerDefinitionDTO = schedulerauthoring.ManagementDefinition
type ManagementSchedulerDefinitionVersionDTO = schedulerauthoring.ManagementDefinitionVersion
type ManagementSchedulerAuthoringContract = schedulerauthoring.ManagementAuthoringContract

func (s *SchedulerApplicationService) ManagementDefinitions(ctx context.Context, principal principalmodel.Principal) ([]ManagementSchedulerDefinitionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionListManagementSchedulerDefinitions); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, unavailableDefinitionSource()
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		return nil, internalError("list tenant admin scheduler definitions", err)
	}
	out := make([]ManagementSchedulerDefinitionDTO, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, projectManagementSchedulerDefinition(definition))
	}
	return out, nil
}

func (s *SchedulerApplicationService) ManagementDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (ManagementSchedulerDefinitionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetManagementSchedulerDefinition); err != nil {
		return ManagementSchedulerDefinitionDTO{}, err
	}
	definition, err := s.schedulerDefinition(ctx, definitionID)
	if err != nil {
		return ManagementSchedulerDefinitionDTO{}, err
	}
	return projectManagementSchedulerDefinition(definition), nil
}

func (s *SchedulerApplicationService) ManagementDefinitionVersions(ctx context.Context, definitionID string, principal principalmodel.Principal) ([]ManagementSchedulerDefinitionVersionDTO, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetManagementSchedulerDefinition); err != nil {
		return nil, err
	}
	if _, err := s.schedulerDefinition(ctx, definitionID); err != nil {
		return nil, err
	}
	versions, err := s.definitions.ListSchedulerDefinitionVersions(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	out := make([]ManagementSchedulerDefinitionVersionDTO, 0, len(versions))
	for _, version := range versions {
		out = append(out, ManagementSchedulerDefinitionVersionDTO{
			VersionID: version.VersionID,
			Event:     version.Event,
			CreatedAt: version.CreatedAt,
			Value:     projectManagementSchedulerDefinition(PublishedDefinition{Key: definitionID, Data: version.Data, CreatedAt: version.CreatedAt}),
		})
	}
	return out, nil
}

func (s *SchedulerApplicationService) ManagementAuthoringContract(_ context.Context, principal principalmodel.Principal) (ManagementSchedulerAuthoringContract, error) {
	if err := schedulerExactQueryAllowed(principal, ActionGetManagementSchedulerAuthoringContract); err != nil {
		return ManagementSchedulerAuthoringContract{}, err
	}
	return schedulerauthoring.ManagementContract(), nil
}

func (s *SchedulerApplicationService) AuthorizeOpsRead(ctx context.Context, principal principalmodel.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return schedulerExactQueryAllowed(principal, ActionGetOpsSchedulerState)
}

func projectManagementSchedulerDefinition(definition PublishedDefinition) ManagementSchedulerDefinitionDTO {
	return schedulerauthoring.ProjectManagementDefinition(schedulerauthoring.DefinitionProjection{Key: definition.Key, Data: definition.Data, CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt})
}

func unavailableDefinitionSource() error {
	return schedulerErrorUnavailable("backend.scheduler.definition_source_unavailable")
}

func schedulerErrorUnavailable(code string) error {
	return schedulerError(apperror.KindUnavailable, code, nil)
}
