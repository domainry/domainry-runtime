package party

import (
	"context"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type PartyApplicationService struct {
	domain *partyservice.PartyDomainService
}

func NewPartyApplicationService(domain *partyservice.PartyDomainService) *PartyApplicationService {
	return &PartyApplicationService{domain: domain}
}

func (s *PartyApplicationService) List(ctx context.Context, principal principalmodel.Principal) ([]partymodel.Aggregate, error) {
	if err := authorizeParty(principal, "party.read"); err != nil {
		return nil, err
	}
	return s.domain.List(ctx, principal.WorkspaceID)
}

func (s *PartyApplicationService) Get(ctx context.Context, partyID string, principal principalmodel.Principal) (partymodel.Aggregate, bool, error) {
	if err := authorizeParty(principal, "party.read"); err != nil {
		return partymodel.Aggregate{}, false, err
	}
	return s.domain.Get(ctx, principal.WorkspaceID, partyID)
}

func (s *PartyApplicationService) Upsert(ctx context.Context, value partymodel.Aggregate, principal principalmodel.Principal) (partymodel.Aggregate, error) {
	if err := authorizeParty(principal, "party.write"); err != nil {
		return partymodel.Aggregate{}, err
	}
	return s.domain.Upsert(ctx, principal.WorkspaceID, value)
}

func authorizeParty(principal principalmodel.Principal, permission string) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	if !principal.HasPermission(permission) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	return nil
}
