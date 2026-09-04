package projection

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRecordFeaturePermissionProjectionPublishesSDKDecisions(t *testing.T) {
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-a"}},
		accessfixture.Bundle{
			Key: "operator", Permissions: []string{"case.read", "case.update", "case.approve", "case.export", "workflow.approval.run"},
			DataPolicies:  []accessfixture.DataPolicyFixture{{ObjectKey: "case", Scope: "all", Read: true, Write: true}},
			FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "case", FieldKey: "name", Read: true, Write: true, Export: true}},
		},
	)
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "retired", DisabledAt: "now"}}}
	actions := []definitionmodel.ActionSchema{{Key: "case.approve", ObjectKey: "case", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp", "otp", "workflow_approval"}}}}

	workflows := []definitionmodel.WorkflowSchema{{Key: "disabled", Enabled: false}, {Key: "approval", Enabled: true}}
	snapshot, err := RecordBuildFeaturePermissions([]definitionmodel.ObjectSchema{object}, actions, workflows, principal)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RoleKey != "operator" || snapshot.UserID != "user-1" || len(snapshot.Objects) != 1 || len(snapshot.Actions) != 1 || len(snapshot.Fields) != 1 || len(snapshot.Exports) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if !snapshot.Actions[0].Allowed || snapshot.Actions[0].Reason != "identity_policy_allowed" || len(snapshot.Actions[0].DataScopes) != 1 || snapshot.Actions[0].DataScopes[0] != identitysdk.DataScopeAll {
		t.Fatalf("action decision=%+v", snapshot.Actions[0])
	}
	if len(snapshot.Actions[0].AssuranceRequired) != 2 || snapshot.Actions[0].AssuranceRequired[0] != "otp" || snapshot.Actions[0].AssuranceRequired[1] != "workflow_approval" {
		t.Fatalf("assurance=%v", snapshot.Actions[0].AssuranceRequired)
	}
	if snapshot.Fields[0].Source != "identity_policy" || !snapshot.Fields[0].Read.Allowed || !snapshot.Fields[0].Write.Allowed || !snapshot.Fields[0].Export.Allowed {
		t.Fatalf("field decision=%+v", snapshot.Fields[0])
	}
	if len(snapshot.Functions) != 5 || snapshot.Functions[0].Decision.Reason != "identity_policy" || len(snapshot.Workflows) != 1 || snapshot.Workflows[0].Key != "workflow.approval.run" || snapshot.Workflows[0].PermissionKey != snapshot.Workflows[0].Key || !snapshot.Workflows[0].Allowed {
		t.Fatalf("functions=%+v workflows=%+v", snapshot.Functions, snapshot.Workflows)
	}
}

func TestRecordFeaturePermissionProjectionFailsClosedAndSupportsSystemPrincipal(t *testing.T) {
	if _, err := RecordBuildFeaturePermissions(nil, nil, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.role.unknown" {
		t.Fatalf("unknown principal err=%v", err)
	}
	system := principalmodel.NewSystemPrincipal("runtime-worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "projection"), "case.read")
	snapshot, err := RecordBuildFeaturePermissions([]definitionmodel.ObjectSchema{{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, nil, nil, system)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Objects[0].Actions[0].Allowed || snapshot.Objects[0].Actions[0].Reason != "runtime_system_capability" || snapshot.Fields[0].Source != "runtime_system_capability" {
		t.Fatalf("system projection=%+v", snapshot)
	}
}

func TestRecordPermissionProjectionHelpers(t *testing.T) {
	if operation, ok := approvalOperation(definitionmodel.ActionSchema{Key: "approve_request"}); !ok || operation != "approve" {
		t.Fatalf("approval operation=%q/%v", operation, ok)
	}
	if got := actionAssuranceRequired(definitionmodel.ActionSchema{}); got == nil || len(got) != 0 {
		t.Fatalf("empty assurance=%v", got)
	}
	if objectKey, action := definitionmodel.ActionPermissionSubject(definitionmodel.ActionSchema{Key: "case.transition.start", ObjectKey: "case"}); objectKey != "case" || action != "transition.start" {
		t.Fatalf("split=%q/%q", objectKey, action)
	}
}
