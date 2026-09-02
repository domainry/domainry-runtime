package appschema

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataSchemaApplicationProviderStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s metadataSchemaApplicationProviderStub) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

func TestApplicationSchemaQueryApplicationServiceOwnsFeaturePermissionProjection(t *testing.T) {
	application := NewApplicationSchemaQueryApplicationService(metadataSchemaApplicationProviderStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}}, nil)
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "admin"}}, accessfixture.Bundle{
		Permissions:  []string{"customer.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
	})
	permissions, err := application.FeaturePermissions(t.Context(), admin)
	if err != nil || len(permissions.Objects) != 1 || !permissions.Objects[0].Actions[0].Allowed {
		t.Fatalf("permissions=%#v err=%v", permissions, err)
	}
	if _, err := application.FeaturePermissions(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal error=%v", err)
	}
}

func TestApplicationSchemaObjectRecordCountRequiresItsExactRuntimeAction(t *testing.T) {
	service := &ApplicationSchemaApplicationService{runtime: localizedLifecycleRuntimeStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer"}},
	}}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{"runtime.appschema.metadata_migration_plan"}})
	if _, err := service.ApplicationSchemaObjectRecordCount(t.Context(), "customer", principal); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("sibling Action error=%v", err)
	}
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{metadataObjectRecordCountAction}})
	if _, err := service.ApplicationSchemaObjectRecordCount(t.Context(), "customer", principal); apperror.CodeOf(err) == "auth.permission_denied" {
		t.Fatalf("exact Action did not cross the authorization boundary: %v", err)
	}
}
