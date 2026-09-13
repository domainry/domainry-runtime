package service

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestBusinessReceiptPermissionsAreSourceOwnedAndIndependent(t *testing.T) {
	registry, err := BuildAuthorizationRegistry(AuthorizationRegistryInput{ApplicationKey: "orders", Snapshot: appschemamodel.ApplicationSchemaSnapshot{
		Actions:   []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, Label: "Approve"}},
		Workflows: []definitionmodel.WorkflowSchema{{Key: "review", Name: "Review", Enabled: true}, {Key: "disabled"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for key, binding := range map[string]string{"action.receipt.order.approve.read": "runtime_action_receipt_read", "workflow.receipt.review.read": "runtime_workflow_receipt_read"} {
		d, ok := registry.Definition(key)
		if !ok || d.Permission == nil || d.Owner != "application:orders" || d.Permission.Owner != d.Owner || d.Permission.Key != key || d.Permission.ResourceKey+".read" != key || d.EffectClass != actioncontract.EffectRead || d.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || len(d.NonHTTP) != 1 || d.NonHTTP[0].Kind != binding || d.HTTP != nil {
			t.Fatal("receipt policy is not an independent owner read", key, d)
		}
	}
	if _, ok := registry.Definition("workflow.receipt.disabled.read"); ok {
		t.Fatal("disabled workflow exposed receipt permission")
	}
	for _, key := range []string{"order.approve", "workflow.review.run"} {
		d, ok := registry.Definition(key)
		if !ok || d.EffectClass != actioncontract.EffectWrite {
			t.Fatal("original execution policy changed", key, d)
		}
	}
}
