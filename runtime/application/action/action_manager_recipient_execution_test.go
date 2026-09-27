package action

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func managerRecipientExecution(t *testing.T) *businessActionExecution {
	t.Helper()
	permissions := []string{"crm_task.escalate_overdue"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "service"}}, accessfixture.Bundle{
		Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll),
	})
	return &businessActionExecution{
		workspace: runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(),
		invocation: actionmodel.ActionInvocation{Principal: principal},
		action: definitionmodel.ActionSchema{Key: "crm_task.escalate_overdue", ObjectKey: "crm_task", EffectSet: &definitionmodel.ActionEffectSet{
			Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "crm_task", Operations: []string{"get"}}},
		}},
		objectGrants: []runtimeext.ActionObjectCapability{{ObjectKey: "crm_task", Operations: []string{runtimeext.ObjectCapabilityGet, runtimeext.RecordManagerRecipientOperation}}},
		dependencies: BusinessHandlerExecutionDependencies{
			ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) {
				return definitionmodel.ObjectSchema{Key: key, Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}, {Key: "title", Type: "text"}}}, true
			},
			GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
				return recordmodel.Record{ID: "task-1", Data: map[string]any{"owner": "alice"}}, nil
			},
			FindIdentityUser: func(ctx context.Context, id string) (identitysdk.User, bool, error) {
				if requestcontext.WorkspaceID(ctx) != "workspace-a" {
					t.Fatalf("identity lookup escaped workspace: %q", requestcontext.WorkspaceID(ctx))
				}
				switch id {
				case "alice":
					return identitysdk.User{ID: id, ManagerUserID: "manager"}, true, nil
				case "manager":
					return identitysdk.User{ID: id, Status: identitysdk.UserStatusActive}, true, nil
				default:
					return identitysdk.User{}, false, nil
				}
			},
		},
	}
}

func TestRecordManagerRecipientRequiresReadableUserFieldAndActiveWorkspaceManager(t *testing.T) {
	execution := managerRecipientExecution(t)
	request := runtimeext.RecordManagerRecipientRequest{ObjectKey: "crm_task", RecordID: "task-1", UserFieldKey: "owner"}
	manager, err := execution.ResolveRecordManagerRecipient(t.Context(), request)
	if err != nil || manager != "manager" {
		t.Fatalf("manager=%q err=%v", manager, err)
	}
	request.UserFieldKey = "title"
	if _, err := execution.ResolveRecordManagerRecipient(t.Context(), request); err == nil {
		t.Fatal("non-user field was accepted")
	}
	request.UserFieldKey = "owner"
	execution.objectGrants[0].Operations = []string{runtimeext.ObjectCapabilityGet}
	if _, err := execution.ResolveRecordManagerRecipient(t.Context(), request); err == nil {
		t.Fatal("manager lookup without grant was accepted")
	}
}

func TestRecordManagerRecipientDoesNotReturnInactiveManager(t *testing.T) {
	for _, state := range []identitysdk.User{{Status: "disabled"}, {Status: identitysdk.UserStatusActive, WorkStatus: "terminated"}} {
		execution := managerRecipientExecution(t)
		execution.dependencies.FindIdentityUser = func(_ context.Context, id string) (identitysdk.User, bool, error) {
			if id == "alice" {
				return identitysdk.User{ID: id, ManagerUserID: "manager"}, true, nil
			}
			state.ID = id
			return state, true, nil
		}
		manager, err := execution.ResolveRecordManagerRecipient(t.Context(), runtimeext.RecordManagerRecipientRequest{ObjectKey: "crm_task", RecordID: "task-1", UserFieldKey: "owner"})
		if err != nil || manager != "" {
			t.Fatalf("inactive manager=%q err=%v", manager, err)
		}
	}
}
