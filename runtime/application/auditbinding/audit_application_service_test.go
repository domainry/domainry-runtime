package auditbinding

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRuntimeAuditPolicyRequiresExactSourceOwnedBusinessReadAction(t *testing.T) {
	policy := runtimePolicy()
	for _, test := range []struct {
		name       string
		permission string
		allowed    bool
	}{
		{name: "exact business read", permission: auditBusinessReadAction, allowed: true},
		{name: "retired identity audit alias", permission: "identity.audit.view"},
		{name: "sibling audit action", permission: "audit.governance.read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{test.permission}})
			if got := policy.CanView(principal); got != test.allowed {
				t.Fatalf("CanView=%t want=%t for %q", got, test.allowed, test.permission)
			}
		})
	}
}
