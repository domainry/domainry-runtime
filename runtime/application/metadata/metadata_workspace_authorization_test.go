package metadata

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type metadataWorkspaceAuthorizationProbe struct {
	metadatarepository.MetadataRepository
	calls *int
}

func (p metadataWorkspaceAuthorizationProbe) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	*p.calls++
	return nil, nil
}

func (p metadataWorkspaceAuthorizationProbe) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	*p.calls++
	return metadatamodel.MetadataDefinition{}, false, nil
}

func (p metadataWorkspaceAuthorizationProbe) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	*p.calls++
	return manifestmodel.ManifestSchema{}, nil
}

func TestMetadataApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	calls := 0
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: metadataWorkspaceAuthorizationProbe{calls: &calls}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	checks := []func() error{
		func() error {
			_, err := service.ListMetadataDefinitions(t.Context(), "object", "", principal)
			return err
		},
		func() error {
			_, _, err := service.GetMetadataDefinition(t.Context(), "object", "customer", principal)
			return err
		},
		func() error { _, err := service.MetadataMigrationPlan(t.Context(), principal); return err },
		func() error { _, err := service.ReloadMetadata(t.Context(), principal); return err },
	}
	for index, check := range checks {
		if code := apperror.CodeOf(check()); code != "backend.workspace_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	if calls != 0 {
		t.Fatalf("repository called before workspace authorization: %d", calls)
	}
}
