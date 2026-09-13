package projection

import (
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	actionowner "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

func AuthorizationActionForActionReceipt(schema definitionmodel.ActionSchema, owner string) (actioncontract.ActionDefinition, error) {
	return receiptAuthorizationDefinition(actionowner.ReceiptReadActionKey(schema.Key), schema.Key, schema.Label, owner, "action")
}

func AuthorizationActionForWorkflowReceipt(schema definitionmodel.WorkflowSchema, owner string) (actioncontract.ActionDefinition, error) {
	return receiptAuthorizationDefinition(workflowcontract.ReceiptReadActionKey(schema.Key), schema.Key, schema.Name, owner, "workflow")
}

// Registering these permissions only publishes the owner's policy vocabulary.
// Roles must explicitly grant access to original inputs and receipts.
func receiptAuthorizationDefinition(key, sourceKey, label, owner, kind string) (actioncontract.ActionDefinition, error) {
	if label = strings.TrimSpace(label); label == "" {
		label = sourceKey
	}
	resource := strings.TrimSuffix(key, ".read")
	return actioncontract.NormalizeDefinition(actioncontract.ActionDefinition{
		Key: key, Owner: owner, SourceKind: "metadata_" + kind + "_receipt",
		CapabilityKey: resource, CapabilityLabel: label + " receipts",
		OperationKey: "read", OperationLabel: "Read", Label: "Read original inputs and receipts: " + label,
		Exposures:     []actioncontract.Exposure{actioncontract.ExposurePublic},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: "runtime_" + kind + "_receipt_read", InvocationKey: sourceKey}},
		Permission: &actioncontract.PermissionDefinition{
			Key: key, Owner: owner, ResourceKey: resource, OperationKey: "read",
			Label:       "Read original inputs and receipts: " + label,
			Description: "Read owned original invocation inputs and neutral acknowledgements, subject to current source access; does not permit execution or raw handler outputs.",
			Category:    "Business Receipts", LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskMedium,
		IdempotencyDecision: "not_applicable", AuditClass: "business_receipt_read", LifecycleStatus: actioncontract.LifecycleActive,
	})
}
