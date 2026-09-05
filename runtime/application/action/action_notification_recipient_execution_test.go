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
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRecordNotificationRecipientExposesOnlyGrantedRuntimeOwnerIdentity(t *testing.T) {
	execution := &businessActionExecution{
		objectGrants:    []runtimeext.ActionObjectCapability{{ObjectKey: "lead", Operations: []string{runtimeext.RecordNotificationRecipientOperation}}},
		observedRecords: map[string]recordmodel.Record{"lead\x00lead-1": {ID: "lead-1", OwnerUserID: "sales-1", OwnerOrgID: "department-1", Data: map[string]any{"name": "private"}}},
	}
	recipient, err := execution.ResolveRecordNotificationRecipient(context.Background(), runtimeext.RecordNotificationRecipientRequest{ObjectKey: "lead", RecordID: "lead-1"})
	if err != nil || recipient != "sales-1" {
		t.Fatalf("recipient=%q err=%v", recipient, err)
	}
	if _, err := execution.ResolveRecordNotificationRecipient(context.Background(), runtimeext.RecordNotificationRecipientRequest{ObjectKey: "account", RecordID: "lead-1"}); err == nil {
		t.Fatal("ungranted owner-recipient projection was accepted")
	}
}

func TestRecordNotificationRecipientUsesCurrentActionReadAndCallerRecordScope(t *testing.T) {
	permissions := []string{"lead.send_overdue_reminders"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "director-1"}}, accessfixture.Bundle{
		Permissions:  permissions,
		DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeOwner),
	})
	var authorized principalmodel.Principal
	execution := &businessActionExecution{
		action: definitionmodel.ActionSchema{
			Key: "lead.send_overdue_reminders", ObjectKey: "lead",
			EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "lead"}}},
		},
		invocation:   actionmodel.ActionInvocation{Principal: principal},
		objectGrants: []runtimeext.ActionObjectCapability{{ObjectKey: "lead", Operations: []string{runtimeext.RecordNotificationRecipientOperation}}},
		dependencies: BusinessHandlerExecutionDependencies{GetRecord: func(_ context.Context, _ string, recordID string, got principalmodel.Principal) (recordmodel.Record, error) {
			authorized = got
			return recordmodel.Record{ID: recordID, OwnerUserID: "sales-1", OwnerOrgID: "east", Data: map[string]any{"private": "hidden"}}, nil
		}},
	}
	recipient, err := execution.ResolveRecordNotificationRecipient(t.Context(), runtimeext.RecordNotificationRecipientRequest{ObjectKey: "lead", RecordID: "lead-1"})
	if err != nil || recipient != "sales-1" {
		t.Fatalf("recipient=%q err=%v", recipient, err)
	}
	if !authorized.HasPermission("lead.read") || authorized.AccessBundle == nil || string(authorized.AccessBundle.Subject.SubjectID) != "director-1" {
		t.Fatalf("recipient lookup did not preserve caller scope and derived Action read: %+v", authorized)
	}
}
