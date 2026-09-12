package reportmodulehost

import (
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestReportSubjectRoundTripPreservesBothAuthorizationRevisions(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "admin", AuthorizationRevision: "identity-1"}, BusinessAuthorizationRevision: "business-1"}
	hash, err := ReportAccessScopeHash(principal)
	if err != nil {
		t.Fatal(err)
	}
	subject := reportSubjectFromPrincipal(principal, hash)
	// Embedded and serialized owner boundaries must preserve the same evidence.
	raw, err := json.Marshal(subject)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &subject); err != nil {
		t.Fatal(err)
	}
	restored := RuntimePrincipalFromReportSubject(subject)
	actual, err := ReportAccessScopeHash(restored)
	if err != nil || actual != hash || restored.AuthorizationRevision != "identity-1" || restored.BusinessAuthorizationRevision != "business-1" {
		t.Fatalf("report round trip lost revision: %+v hash=%s error=%v", restored, actual, err)
	}
	restored.BusinessAuthorizationRevision = "business-2"
	changed, err := ReportAccessScopeHash(restored)
	if err != nil || changed == hash {
		t.Fatal("Profile change did not invalidate report evidence")
	}
}

func TestReportAccessScopeHashCanonicalizesSetOrderingWithoutWeakeningFacts(t *testing.T) {
	left := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "demo-user", AuthorizationRevision: "revision-1",
		OrgID: "store-a", OrgScopeIDs: []string{"store-a", "region-a"}, ReportingScopeUserIDs: []string{"demo-user", "seller-a"}},
		BusinessProfiles: []profilebindingmodel.Reference{{BindingKey: "member-b", ObjectKey: "member", RecordID: "two"}, {BindingKey: "member-a", ObjectKey: "member", RecordID: "one"}},
	}, accessfixture.Bundle{Key: "group_admin", Permissions: []string{"sales.export", "sales.read"}})
	right := left
	right.BusinessProfiles = []profilebindingmodel.Reference{left.BusinessProfiles[1], left.BusinessProfiles[0]}
	right.OrgScopeIDs = []string{"region-a", "store-a"}
	right.ReportingScopeUserIDs = []string{"seller-a", "demo-user"}
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
	right.AuthorizationRevision = left.AuthorizationRevision
	right.OrgScopeIDs = []string{"region-b", "store-a"}
	organizationHash, err := ReportAccessScopeHash(right)
	if err != nil || organizationHash == leftHash {
		t.Fatalf("organization scope change hash=%s err=%v", organizationHash, err)
	}
}

func TestReportSubjectRoundTripPreservesTrustedProcessAuthority(t *testing.T) {
	principal := principalmodel.NewSystemPrincipal(
		"runtime-target-executor",
		principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "scheduled report snapshot"),
		"report.snapshots.refresh",
		"opportunity.read",
	)
	principal.WorkspaceID = "workspace-primary"
	hash, err := ReportAccessScopeHash(principal)
	if err != nil {
		t.Fatal(err)
	}
	subject := reportSubjectFromPrincipal(principal, hash)
	if !subject.TrustedProcess || !subject.HasAllPermissions([]string{"report.snapshots.refresh", "opportunity.read"}) {
		t.Fatalf("subject=%#v", subject)
	}
	roundTrip := RuntimePrincipalFromReportSubject(subject)
	if !roundTrip.SystemScope.Valid() || !roundTrip.HasPermission("opportunity.read") || roundTrip.HasPermission("opportunity.export") {
		t.Fatalf("round-trip principal=%#v", roundTrip)
	}
	changed := principal.WithExactSystemCapabilities("opportunity.export")
	changedHash, err := ReportAccessScopeHash(changed)
	if err != nil || changedHash == hash {
		t.Fatalf("capability change hash=%s err=%v", changedHash, err)
	}
}
