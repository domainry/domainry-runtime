package preference

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
	preferencerepository "github.com/domainry/domainry-runtime/runtime/domain/preference/repository"
	preferenceservice "github.com/domainry/domainry-runtime/runtime/domain/preference/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type WorkspacePreferenceApplicationService struct {
	repository preferencerepository.WorkspacePreferenceRepository
}

func NewWorkspacePreferenceApplicationService(repository preferencerepository.WorkspacePreferenceRepository) *WorkspacePreferenceApplicationService {
	return &WorkspacePreferenceApplicationService{repository: repository}
}

func (s *WorkspacePreferenceApplicationService) Resolve(ctx context.Context, preferenceKey string, effectiveAt time.Time, principal principalmodel.Principal) (preferencemodel.WorkspacePreferenceResolution, error) {
	if !principal.Known || principal.SystemScope.Valid() {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindForbidden, "backend.preference.workspace_principal_required", nil, nil)
	}
	scope, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID)
	if err != nil {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindBadRequest, "backend.preference.workspace_required", err, nil)
	}
	preferenceKey = strings.TrimSpace(preferenceKey)
	if preferenceKey == "" {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindBadRequest, "backend.preference.key_required", nil, nil)
	}
	if effectiveAt.IsZero() {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindBadRequest, "backend.preference.effective_at_required", nil, map[string]string{"preference_key": preferenceKey})
	}
	if s == nil || s.repository == nil {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindInternal, "backend.preference.repository_unavailable", nil, map[string]string{"preference_key": preferenceKey})
	}
	versions, err := s.repository.ListWorkspacePreferenceVersions(ctx, scope, preferenceKey)
	if err != nil {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.New(apperror.KindInternal, "backend.preference.versions_read_failed", err, map[string]string{"preference_key": preferenceKey})
	}
	resolved, err := preferenceservice.ResolveWorkspacePreference(versions, scope.WorkspaceID().String(), preferenceKey, effectiveAt)
	if err == nil {
		return resolved, nil
	}
	var resolutionErr *preferencemodel.WorkspacePreferenceError
	// ResolveWorkspacePreference owns this closed error contract.
	_ = errors.As(err, &resolutionErr)
	if resolutionErr.Code == "backend.preference.effective_version_not_found" {
		return preferencemodel.WorkspacePreferenceResolution{}, apperror.FromError(apperror.KindNotFound, err)
	}
	return preferencemodel.WorkspacePreferenceResolution{}, apperror.FromError(apperror.KindBadRequest, err)
}
