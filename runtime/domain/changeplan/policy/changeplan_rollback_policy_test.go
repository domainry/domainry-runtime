package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestChangePlanBusinessRollbackPolicyPreservesExecutionEvidenceAndAdministratorSafety(t *testing.T) {
	policy, err := ChangePlanBusinessMaintenanceRollbackPolicy(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}}))
	if err != nil {
		t.Fatal(err)
	}
	foundWorkflow, foundScheduler, foundGovernance, foundManual := false, false, false, false
	for _, resource := range policy.Resources {
		if !resource.PreservesRunEvidence {
			t.Fatalf("rollback policy may not discard execution evidence: %#v", resource)
		}
		for _, resourceType := range resource.ResourceTypes {
			switch resourceType {
			case "workflow":
				foundWorkflow = resource.Strategy == "new_published_version"
			case "scheduler_definition":
				foundScheduler = resource.Strategy == "compensating_change_plan"
			case "role":
				for _, check := range resource.SafetyChecks {
					foundGovernance = foundGovernance || check == "unique_administrator"
				}
			case "external_side_effect":
				foundManual = resource.Strategy == "manual_compensation"
			}
		}
	}
	if !foundWorkflow || !foundScheduler || !foundGovernance || !foundManual {
		t.Fatalf("rollback policy is incomplete: %#v", policy)
	}
}

func TestChangePlanBusinessRollbackPolicyRequiresKnownWorkspaceAdministrator(t *testing.T) {
	for _, principal := range []principalmodel.Principal{principalmodel.Principal{}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.read"}})} {
		policy, err := ChangePlanBusinessMaintenanceRollbackPolicy(principal)
		if apperror.CodeOf(err) != "auth.permission_denied" || policy.Version != "" || policy.Resources != nil {
			t.Fatalf("unauthorized policy = %#v, err=%v", policy, err)
		}
	}
}
