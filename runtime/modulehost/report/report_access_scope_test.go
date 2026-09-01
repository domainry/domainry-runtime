package reportmodulehost

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestReportAccessScopeHashCanonicalizesSetOrderingWithoutWeakeningFacts(t *testing.T) {
	left := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "demo-user", AuthorizationRevision: "revision-1",
		ReportingUserIDs: []string{"user-b", "user-a"}, OrganizationScopes: identitysdk.OrganizationScopes{StoreIDs: []string{"store-b", "store-a"}}},
		BusinessProfiles: []profilebindingmodel.Reference{{BindingKey: "member-b", ObjectKey: "member", RecordID: "two"}, {BindingKey: "member-a", ObjectKey: "member", RecordID: "one"}},
	}, accessfixture.Bundle{Key: "group_admin", Permissions: []string{"sales.export", "sales.read"}})
	right := left
	right.ReportingUserIDs = []string{"user-a", "user-b", "user-a"}
	right.OrganizationScopes.StoreIDs = []string{"store-a", "store-b"}
	right.BusinessProfiles = []profilebindingmodel.Reference{left.BusinessProfiles[1], left.BusinessProfiles[0]}
	leftHash, err := ReportAccessScopeHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := ReportAccessScopeHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("set-order-only principal drift changed hash: left=%s right=%s", leftHash, rightHash)
	}
	right.AuthorizationRevision = "revision-2"
	changedHash, err := ReportAccessScopeHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == leftHash {
		t.Fatal("authorization revision change did not change Report access scope hash")
	}
}
