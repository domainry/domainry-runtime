package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestVisibilityWriteOnlyFieldAndExactActionKeyEdges(t *testing.T) {
	role := accessfixture.Bundle{
		Permissions:  []string{"customer.update", "customer.approve"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Write: true}},
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "customer", FieldKey: "write_only", Write: true},
			{ObjectKey: "customer", FieldKey: "hidden"},
		},
	}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "write_only"}, {Key: "hidden"}}}}}
	filtered := SnapshotForPrincipal(snapshot, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role))
	if len(filtered.Objects) != 1 || len(filtered.Objects[0].Fields) != 1 || filtered.Objects[0].Fields[0].Key != "write_only" {
		t.Fatalf("write-only visibility=%#v", filtered.Objects)
	}
	if actionAllowed(principalmodel.Principal{}, definitionmodel.ActionSchema{}) {
		t.Fatal("unknown principal allowed action")
	}
	if !actionAllowed(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role), definitionmodel.ActionSchema{Key: "customer.approve", ObjectKey: "customer"}) {
		t.Fatal("same-key action permission denied")
	}
	dataDenied := accessfixture.Bundle{Permissions: []string{"customer.read"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer"}}}
	dataDeniedPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, dataDenied)
	if principalCanUseObject(dataDeniedPrincipal, "customer") || principalCanUseAnyObjectAction(dataDeniedPrincipal, map[string]bool{"customer": true}, "read") {
		t.Fatal("data-denied role received object access")
	}
}
