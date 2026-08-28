package service

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func (s *PartyCatalogDomainService) ListOrganizationExtensions(ctx context.Context, workspaceID string) ([]partymodel.OrganizationExtension, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.ListOrganizationExtensions(ctx, workspaceID)
}

func (s *PartyCatalogDomainService) UpsertOrganizationExtension(ctx context.Context, workspaceID string, value partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.OrganizationExtension{}, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	value.ID, value.Kind, value.Code, value.Name = strings.TrimSpace(value.ID), strings.TrimSpace(value.Kind), strings.TrimSpace(value.Code), strings.TrimSpace(value.Name)
	value.ClaimValue = strings.TrimSpace(value.ClaimValue)
	value.ParentID, value.OrganizationUnitID, value.Status = strings.TrimSpace(value.ParentID), strings.TrimSpace(value.OrganizationUnitID), strings.TrimSpace(value.Status)
	if value.ID == "" || value.Code == "" || value.Name == "" || !organizationExtensionKind(value.Kind) {
		return partymodel.OrganizationExtension{}, partyError(apperror.KindBadRequest, "backend.party.organization_extension_invalid")
	}
	if value.Status == "" {
		value.Status = partymodel.PartyStatusActive
	}
	if value.Status != partymodel.PartyStatusActive && value.Status != partymodel.PartyStatusInactive {
		return partymodel.OrganizationExtension{}, partyError(apperror.KindBadRequest, "backend.party.organization_extension_status_invalid")
	}
	if value.ParentID == value.ID {
		return partymodel.OrganizationExtension{}, partyError(apperror.KindBadRequest, "backend.party.organization_extension_self_parent")
	}
	if value.ParentID != "" {
		parent, found, err := s.repository.GetOrganizationExtension(ctx, workspaceID, value.ParentID)
		if err != nil {
			return partymodel.OrganizationExtension{}, err
		}
		if !found || parent.Kind != value.Kind {
			return partymodel.OrganizationExtension{}, partyError(apperror.KindBadRequest, "backend.party.organization_extension_parent_invalid")
		}
	}
	return s.repository.UpsertOrganizationExtension(ctx, workspaceID, value)
}

func (s *PartyCatalogDomainService) ListOrganizationExtensionMemberships(ctx context.Context, workspaceID, workforceProfileID string) ([]partymodel.OrganizationExtensionMembership, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.ListOrganizationExtensionMemberships(ctx, workspaceID, strings.TrimSpace(workforceProfileID))
}

func (s *PartyCatalogDomainService) UpsertOrganizationExtensionMembership(ctx context.Context, workspaceID string, value partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.OrganizationExtensionMembership{}, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	value.ID, value.ExtensionID = strings.TrimSpace(value.ID), strings.TrimSpace(value.ExtensionID)
	value.WorkforceProfileID, value.EffectiveFrom = strings.TrimSpace(value.WorkforceProfileID), strings.TrimSpace(value.EffectiveFrom)
	value.EffectiveTo, value.Status = strings.TrimSpace(value.EffectiveTo), strings.TrimSpace(value.Status)
	if value.ID == "" || value.ExtensionID == "" || value.WorkforceProfileID == "" {
		return partymodel.OrganizationExtensionMembership{}, partyError(apperror.KindBadRequest, "backend.party.organization_membership_invalid")
	}
	if value.Status == "" {
		value.Status = partymodel.PartyStatusActive
	}
	if value.Status != partymodel.PartyStatusActive && value.Status != partymodel.PartyStatusInactive {
		return partymodel.OrganizationExtensionMembership{}, partyError(apperror.KindBadRequest, "backend.party.organization_membership_status_invalid")
	}
	extension, found, err := s.repository.GetOrganizationExtension(ctx, workspaceID, value.ExtensionID)
	if err != nil {
		return partymodel.OrganizationExtensionMembership{}, err
	}
	if !found || extension.Status != partymodel.PartyStatusActive {
		return partymodel.OrganizationExtensionMembership{}, partyError(apperror.KindBadRequest, "backend.party.organization_extension_not_active")
	}
	return s.repository.UpsertOrganizationExtensionMembership(ctx, workspaceID, value)
}

func organizationExtensionKind(value string) bool {
	switch value {
	case partymodel.OrganizationExtensionTerritory, partymodel.OrganizationExtensionTeam,
		partymodel.OrganizationExtensionStore, partymodel.OrganizationExtensionWarehouse:
		return true
	default:
		return false
	}
}
