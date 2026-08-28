package service

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyrepository "github.com/domainry/domainry-runtime/runtime/domain/party/repository"
)

type PartyCatalogDomainService struct {
	repository partyrepository.PartyCatalogRepository
}

func NewPartyCatalogDomainService(repository partyrepository.PartyCatalogRepository) *PartyCatalogDomainService {
	return &PartyCatalogDomainService{repository: repository}
}

func (s *PartyCatalogDomainService) ListJobs(ctx context.Context, workspaceID string) ([]partymodel.JobCatalogItem, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.ListJobs(ctx, workspaceID)
}

func (s *PartyCatalogDomainService) GetJob(ctx context.Context, workspaceID, id string) (partymodel.JobCatalogItem, bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.JobCatalogItem{}, false, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.GetJob(ctx, workspaceID, strings.TrimSpace(id))
}

func (s *PartyCatalogDomainService) UpsertJob(ctx context.Context, workspaceID string, value partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.JobCatalogItem{}, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	value.ID, value.Code, value.Name = strings.TrimSpace(value.ID), strings.TrimSpace(value.Code), strings.TrimSpace(value.Name)
	value.Family, value.Level = strings.TrimSpace(value.Family), strings.TrimSpace(value.Level)
	value.Description, value.Status = strings.TrimSpace(value.Description), strings.TrimSpace(value.Status)
	if value.ID == "" || value.Code == "" || value.Name == "" {
		return partymodel.JobCatalogItem{}, partyError(apperror.KindBadRequest, "backend.party.job_identity_required")
	}
	if value.Status == "" {
		value.Status = partymodel.PartyStatusActive
	}
	if value.Status != partymodel.PartyStatusActive && value.Status != partymodel.PartyStatusInactive {
		return partymodel.JobCatalogItem{}, partyError(apperror.KindBadRequest, "backend.party.job_status_invalid")
	}
	return s.repository.UpsertJob(ctx, workspaceID, value)
}

func (s *PartyCatalogDomainService) ListPositions(ctx context.Context, workspaceID string) ([]partymodel.Position, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.ListPositions(ctx, workspaceID)
}

func (s *PartyCatalogDomainService) GetPosition(ctx context.Context, workspaceID, id string) (partymodel.Position, bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.Position{}, false, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.GetPosition(ctx, workspaceID, strings.TrimSpace(id))
}

func (s *PartyCatalogDomainService) UpsertPosition(ctx context.Context, workspaceID string, value partymodel.Position) (partymodel.Position, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.Position{}, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	value.ID, value.Code, value.Name = strings.TrimSpace(value.ID), strings.TrimSpace(value.Code), strings.TrimSpace(value.Name)
	value.JobCatalogItemID = strings.TrimSpace(value.JobCatalogItemID)
	value.OrganizationUnitID = strings.TrimSpace(value.OrganizationUnitID)
	value.EffectiveFrom, value.EffectiveTo, value.Status = strings.TrimSpace(value.EffectiveFrom), strings.TrimSpace(value.EffectiveTo), strings.TrimSpace(value.Status)
	if value.ID == "" || value.Code == "" || value.Name == "" || value.JobCatalogItemID == "" || value.Headcount < 1 {
		return partymodel.Position{}, partyError(apperror.KindBadRequest, "backend.party.position_invalid")
	}
	if value.Status == "" {
		value.Status = partymodel.PartyStatusActive
	}
	if value.Status != partymodel.PartyStatusActive && value.Status != partymodel.PartyStatusInactive {
		return partymodel.Position{}, partyError(apperror.KindBadRequest, "backend.party.position_status_invalid")
	}
	if _, found, err := s.repository.GetJob(ctx, workspaceID, value.JobCatalogItemID); err != nil {
		return partymodel.Position{}, err
	} else if !found {
		return partymodel.Position{}, partyError(apperror.KindBadRequest, "backend.party.position_job_not_found")
	}
	return s.repository.UpsertPosition(ctx, workspaceID, value)
}
