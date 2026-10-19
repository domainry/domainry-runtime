package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestParentReadProjectsDependentFieldsWithoutGrantingIndependentObjects(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"deal.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"deal.read"}, identitysdk.DataScopeAll)})
	child := definitionmodel.ObjectSchema{Key: "event", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: "deal", RelationField: "deal"}, Fields: []definitionmodel.FieldSchema{{Key: "title", Type: "text"}, {Key: "secret", Type: "text", Sensitive: true}}}
	data := map[string]any{"title": "stage changed", "secret": "private"}
	filtered := RecordFilterReadableData(principal, child, data)
	if filtered["title"] != "stage changed" || filtered["secret"] != nil {
		t.Fatalf("invalid dependent projection: %v", filtered)
	}
	for _, field := range principal.AccessBundle.FieldPolicies {
		if field.Resource == "event" {
			t.Fatal("original principal was mutated")
		}
	}
	independent := child
	independent.AuthorizationParent = nil
	if len(RecordFilterReadableData(principal, independent, data)) != 0 {
		t.Fatal("parent read leaked into an independent object")
	}
	bundle := *principal.AccessBundle
	bundle.FieldPolicies = append(append([]identitysdk.FieldPolicy(nil), bundle.FieldPolicies...), identitysdk.FieldPolicy{Resource: "event", Field: "title", Read: false})
	principal.AccessBundle = &bundle
	if RecordFilterReadableData(principal, child, data)["title"] != nil {
		t.Fatal("explicit field closure was bypassed")
	}
}
