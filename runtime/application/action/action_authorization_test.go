package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestActionAuthorizationAndPersistenceAuthority(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"leave_request.recover"}})
	action := definitionmodel.ActionSchema{Key: "leave_request.recover", ObjectKey: "leave_request"}
	if !ActionAllowed(principal, action) {
		t.Fatal("authorized Action was rejected")
	}
	persist := ActionPersistencePrincipal(principal, "leave_request")
	if !persist.HasPermission("leave_request.update") || principal.HasPermission("leave_request.update") || persist.HasPermission("employee_hr_profile.update") {
		t.Fatalf("unexpected persistence authority: caller=%#v persistence=%#v", principal.PermissionKeys(), persist.PermissionKeys())
	}
}

func TestHandlerActionAuthorizationUsesWriteDataPolicyForExactCustomAction(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "member.self_enroll", ObjectKey: "member",
	}
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "wechat-user", WorkspaceID: "workspace-primary"}},
		accessfixture.Bundle{
			Key: "member_onboarding", Permissions: []string{"member.self_enroll"},
			DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "member", Scope: "owned_records", Write: true}},
		},
	)
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{{Key: "member"}} },
	})
	if err := (ActionAuthorization{ObjectForAction: queryPolicy.ObjectForAction}).Validate(principal, action); err != nil {
		t.Fatalf("custom action with exact function grant and write data policy was rejected: %v", err)
	}
}

func TestHandlerActionAuthorizationUsesOnlyExactPermission(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "booking.book", ObjectKey: "booking"}
	member := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "member", Permissions: []string{"booking.book"}})
	if !ActionAllowed(member, action) {
		t.Fatal("role with the exact Action permission was rejected")
	}
	coach := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "coach", Permissions: []string{"booking.book"}})
	if !ActionAllowed(coach, action) {
		t.Fatal("role key became a second authorization authority")
	}
	memberWithoutPermission := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "member", Permissions: []string{"booking.read"}})
	if ActionAllowed(memberWithoutPermission, action) {
		t.Fatal("role without the exact Action permission was authorized")
	}
}
