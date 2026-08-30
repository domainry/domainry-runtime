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
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "admin"}}, accessfixture.Bundle{
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
