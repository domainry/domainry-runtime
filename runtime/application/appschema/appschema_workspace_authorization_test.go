package appschema

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataWorkspaceAuthorizationProbe struct {
	appschemarepository.ApplicationSchemaRepository
	calls *int
}

func (p metadataWorkspaceAuthorizationProbe) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]appschemamodel.ApplicationDefinition, error) {
	*p.calls++
	return nil, nil
}

func (p metadataWorkspaceAuthorizationProbe) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error) {
	*p.calls++
	return appschemamodel.ApplicationDefinition{}, false, nil
}

func (p metadataWorkspaceAuthorizationProbe) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	*p.calls++
	return manifestmodel.ManifestSchema{}, nil
}

func TestMetadataApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	calls := 0
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataWorkspaceAuthorizationProbe{calls: &calls}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	checks := []func() error{
		func() error {
			_, err := service.ListApplicationDefinitions(t.Context(), "object", "", principal)
			return err
		},
		func() error {
			_, _, err := service.GetApplicationDefinition(t.Context(), "object", "customer", principal)
			return err
		},
		func() error { _, err := service.ApplicationSchemaMigrationPlan(t.Context(), principal); return err },
		func() error { _, err := service.ReloadApplicationSchema(t.Context(), principal); return err },
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
