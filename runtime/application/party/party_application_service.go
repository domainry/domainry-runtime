package party

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	partysdk "github.com/domainry/domainry-party-sdk"
	partymodel "github.com/domainry/domainry-party-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// PartyApplicationService is the Runtime authorization adapter. Party domain
// behavior and persistence live behind the deployment-neutral SDK Binding.
type PartyApplicationService struct{ directory partysdk.Directory }

func NewPartyApplicationService(binding partysdk.Binding) *PartyApplicationService {
	var directory partysdk.Directory
	if binding != nil {
		directory = binding.Directory()
	}
	return &PartyApplicationService{directory: directory}
}
func (s *PartyApplicationService) List(ctx context.Context, principal principalmodel.Principal) ([]partymodel.Aggregate, error) {
	if err := authorizeParty(principal, "party.read"); err != nil {
		return nil, err
	}
	if s.directory == nil {
		return nil, partyUnavailable()
	}
	return s.directory.List(ctx)
}
func (s *PartyApplicationService) Get(ctx context.Context, id string, principal principalmodel.Principal) (partymodel.Aggregate, bool, error) {
	if err := authorizeParty(principal, "party.read"); err != nil {
		return partymodel.Aggregate{}, false, err
	}
	if s.directory == nil {
		return partymodel.Aggregate{}, false, partyUnavailable()
	}
	return s.directory.Get(ctx, id)
}
func (s *PartyApplicationService) Upsert(ctx context.Context, value partymodel.Aggregate, principal principalmodel.Principal) (partymodel.Aggregate, error) {
	if err := authorizeParty(principal, "party.write"); err != nil {
		return partymodel.Aggregate{}, err
	}
	if s.directory == nil {
		return partymodel.Aggregate{}, partyUnavailable()
	}
	return s.directory.Upsert(ctx, value)
}
func partyUnavailable() error {
	return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.party.unavailable"}
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
func authorizePartyCatalog(principal principalmodel.Principal, permission string) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	if !principal.HasPermission("workspace.admin") && !principal.HasPermission(permission) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	return nil
}
