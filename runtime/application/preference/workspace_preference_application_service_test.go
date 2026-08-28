package preference

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type workspacePreferenceRepositoryStub struct {
	versions []preferencemodel.WorkspacePreferenceVersion
	err      error
	scope    principalmodel.QueryScope
	key      string
}

func (r *workspacePreferenceRepositoryStub) ListWorkspacePreferenceVersions(_ context.Context, scope principalmodel.QueryScope, key string) ([]preferencemodel.WorkspacePreferenceVersion, error) {
	r.scope, r.key = scope, key
	return append([]preferencemodel.WorkspacePreferenceVersion(nil), r.versions...), r.err
}

func TestWorkspacePreferenceApplicationResolvePreservesWorkspaceAndVersionEvidence(t *testing.T) {
	t.Parallel()

	repository := &workspacePreferenceRepositoryStub{versions: []preferencemodel.WorkspacePreferenceVersion{{
		WorkspaceID: "workspace-a", Version: "7", ResourceHash: "hash-7",
		Definition: preferencemodel.WorkspacePreferenceDefinition{Key: "policy.limit", Name: "Limit", ValueType: "integer", Value: json.RawMessage(`12`), EffectiveFrom: "2026-07-01"},
	}}}
	service := NewWorkspacePreferenceApplicationService(repository)
	resolved, err := service.Resolve(t.Context(), " policy.limit ", time.Date(2026, 7, 21, 3, 4, 5, 0, time.UTC), workspacePreferencePrincipal("workspace-a"))
	if err != nil {
		t.Fatalf("resolve preference: %v", err)
	}
	if !repository.scope.Valid() || repository.scope.WorkspaceID().String() != "workspace-a" || repository.key != "policy.limit" {
		t.Fatalf("repository scope=%+v key=%q", repository.scope, repository.key)
	}
	if resolved.WorkspaceID != "workspace-a" || resolved.Version != "7" || resolved.ResourceHash != "hash-7" || resolved.EffectiveAt != "2026-07-21T03:04:05Z" {
		t.Fatalf("resolution=%+v", resolved)
	}
}

func TestWorkspacePreferenceApplicationResolveRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		service   *WorkspacePreferenceApplicationService
		key       string
		effective time.Time
		principal principalmodel.Principal
		code      string
	}{
		{name: "unknown principal", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), key: "policy", effective: now, code: "backend.preference.workspace_principal_required"},
		{name: "system principal", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), key: "policy", effective: now, principal: principalmodel.NewSystemPrincipal("system", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test")), code: "backend.preference.workspace_principal_required"},
		{name: "workspace required", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), key: "policy", effective: now, principal: workspacePreferencePrincipal(""), code: "backend.preference.workspace_required"},
		{name: "key required", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.key_required"},
		{name: "effective required", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), key: "policy", principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.effective_at_required"},
		{name: "repository required", service: NewWorkspacePreferenceApplicationService(nil), key: "policy", effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.repository_unavailable"},
		{name: "nil service", service: nil, key: "policy", effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.repository_unavailable"},
		{name: "repository failure", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{err: errors.New("read failed")}), key: "policy", effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.versions_read_failed"},
		{name: "version absent", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{}), key: "policy", effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.effective_version_not_found"},
		{name: "invalid version", service: NewWorkspacePreferenceApplicationService(&workspacePreferenceRepositoryStub{versions: []preferencemodel.WorkspacePreferenceVersion{{WorkspaceID: "workspace-a", Version: "1", Definition: preferencemodel.WorkspacePreferenceDefinition{Key: "policy", EffectiveFrom: "invalid"}}}}), key: "policy", effective: now, principal: workspacePreferencePrincipal("workspace-a"), code: "backend.preference.effective_from_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.service.Resolve(t.Context(), test.key, test.effective, test.principal)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("expected %s, got %v (%s)", test.code, err, apperror.CodeOf(err))
			}
		})
	}
}

func workspacePreferencePrincipal(workspaceID string) principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: "user-a"}}
}
