package action

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestActionReadEffectAuthorizationUsesOnlyAuthorizationParentACL(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "support_case"}
	child := definitionmodel.ObjectSchema{
		Key: "case_thread_link",
		Fields: []definitionmodel.FieldSchema{{
			Key: "support_case", Type: "relation", Required: true,
			Validation: definitionmodel.FieldValidation{Target: parent.Key},
		}},
		AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: parent.Key, RelationField: "support_case"},
	}
	objects := map[string]definitionmodel.ObjectSchema{parent.Key: parent, child.Key: child}
	objectForKey := func(key string) (definitionmodel.ObjectSchema, bool) {
		object, found := objects[key]
		return object, found
	}
	action := definitionmodel.ActionSchema{Key: "support_case.ingest_email", ObjectKey: parent.Key}
	effects := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: child.Key}}}
	action.EffectSet = effects
	original := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "support-agent"}},
		accessfixture.Bundle{
			Permissions:  []string{action.Key},
			DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: parent.Key, Action: "ingest_email", Scope: identitysdk.DataScopeOwner, Write: true}},
		},
	)

	var authorized principalmodel.Principal
	execution := &businessActionExecution{
		invocation: actionmodel.ActionInvocation{Principal: original},
		action:     action,
		dependencies: BusinessHandlerExecutionDependencies{
			ObjectForKey: objectForKey,
			ListRecords: func(_ context.Context, objectKey string, _ recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				if objectKey != child.Key {
					t.Fatalf("queried object=%q", objectKey)
				}
				authorized = principal
				return recordmodel.RecordPageResult{}, nil
			},
		},
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: child.Key, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if !authorized.HasPermission(parent.Key + ".read") {
		t.Fatal("authorization parent read authority was not derived")
	}
	if authorized.HasPermission(child.Key + ".read") {
		t.Fatal("dependent object received an independent read ACL")
	}
	if original.HasPermission(parent.Key+".read") || original.HasPermission(child.Key+".read") {
		t.Fatal("source principal was mutated")
	}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{parent, child} },
	})
	if _, err := policy.ObjectForAction(authorized, child.Key, "read"); err != nil {
		t.Fatalf("parent-authorized dependent read was rejected: %v", err)
	}

	// The parent authority exists only inside the dependent query. The Handler
	// still cannot query the parent unless its EffectSet declares that object.
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: parent.Key, Limit: 1}); err == nil {
		t.Fatal("authorization parent became an undeclared Handler query capability")
	}
}

func TestActionReadEffectAuthorizationParentSupportsSystemPrincipal(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "support_case"}
	child := definitionmodel.ObjectSchema{Key: "case_thread_link", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: parent.Key, RelationField: "support_case"}}
	objects := map[string]definitionmodel.ObjectSchema{parent.Key: parent, child.Key: child}
	objectForKey := func(key string) (definitionmodel.ObjectSchema, bool) {
		object, found := objects[key]
		return object, found
	}
	action := definitionmodel.ActionSchema{Key: "support_case.ingest_email", ObjectKey: parent.Key}
	effects := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: child.Key}}}
	original := principalmodel.NewSystemPrincipal("integration-worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute integration action"), action.Key)

	authorized := actionReadEffectAuthorizationPrincipal(original, effects, action, child.Key, objectForKey)
	if !authorized.HasPermission(parent.Key+".read") || authorized.HasPermission(child.Key+".read") {
		t.Fatalf("system capabilities=%v", authorized.PermissionKeys())
	}
	if original.HasPermission(parent.Key + ".read") {
		t.Fatal("source system principal was mutated")
	}
}

func TestActionReadEffectAuthorizationParentFailsClosedWhenParentCannotResolve(t *testing.T) {
	child := definitionmodel.ObjectSchema{Key: "case_thread_link", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: "missing_case", RelationField: "support_case"}}
	objectForKey := func(key string) (definitionmodel.ObjectSchema, bool) {
		if key == child.Key {
			return child, true
		}
		return definitionmodel.ObjectSchema{}, false
	}
	action := definitionmodel.ActionSchema{Key: "support_case.ingest_email", ObjectKey: "support_case"}
	effects := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: child.Key}}}
	principal := principalmodel.NewSystemPrincipal("integration-worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute integration action"), action.Key)

	authorized := actionReadEffectAuthorizationPrincipal(principal, effects, action, child.Key, objectForKey)
	if authorized.HasPermission(child.Key+".read") || authorized.HasPermission("missing_case.read") {
		t.Fatalf("invalid parent model manufactured authority: %v", authorized.PermissionKeys())
	}
}
