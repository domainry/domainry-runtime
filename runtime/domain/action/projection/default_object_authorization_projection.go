package projection

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// DefaultActionsForObject is the single Runtime policy that turns a
// published ObjectSchema into its executable record Actions. Generic object
// HTTP routes resolve their path object/operation to these exact non-HTTP
// invocation identities before applying the ordinary same-key Permission gate.
func DefaultActionsForObject(object definitionmodel.ObjectSchema, owner string) ([]actioncontract.ActionDefinition, error) {
	objectKey, owner := strings.TrimSpace(object.Key), strings.TrimSpace(owner)
	if objectKey == "" || owner == "" {
		return nil, fmt.Errorf("default object Actions require object key and owner")
	}
	capabilityLabel := strings.TrimSpace(object.Name)
	if capabilityLabel == "" {
		capabilityLabel = objectKey
	}
	operations := []struct {
		key, label, idempotency string
		method, route           string
		effect                  actioncontract.EffectClass
		risk                    actioncontract.RiskLevel
	}{
		{key: "create", label: "Create", method: "POST", route: "/objects/{objectKey}/records", idempotency: "caller_key_required", effect: actioncontract.EffectWrite, risk: actioncontract.RiskMedium},
		{key: "read", label: "Read", method: "GET", route: "/objects/{objectKey}/records", idempotency: "not_applicable", effect: actioncontract.EffectRead, risk: actioncontract.RiskLow},
		{key: "update", label: "Update", method: "PATCH", route: "/objects/{objectKey}/records/{recordID}", idempotency: "optimistic_concurrency", effect: actioncontract.EffectWrite, risk: actioncontract.RiskMedium},
		{key: "delete", label: "Delete", method: "DELETE", route: "/objects/{objectKey}/records/{recordID}", idempotency: "optimistic_concurrency", effect: actioncontract.EffectWrite, risk: actioncontract.RiskHigh},
		{key: "export", label: "Export", method: "GET", route: "/objects/{objectKey}/records/export", idempotency: "not_applicable", effect: actioncontract.EffectRead, risk: actioncontract.RiskMedium},
	}
	capabilities := definitionmodel.EffectiveObjectCapabilities(object)
	enabled := map[string]bool{
		"create": capabilities.Create, "read": capabilities.Read,
		"update": capabilities.Update, "delete": capabilities.Delete,
		"export": capabilities.Export,
	}
	definitions := make([]actioncontract.ActionDefinition, 0, len(operations))
	for _, operation := range operations {
		if !enabled[operation.key] {
			continue
		}
		key := objectKey + "." + operation.key
		definition, err := actioncontract.NormalizeDefinition(actioncontract.ActionDefinition{
			Key: key, Owner: owner, SourceKind: "object_default",
			CapabilityKey: objectKey, CapabilityLabel: capabilityLabel,
			OperationKey: operation.key, OperationLabel: operation.label, Label: capabilityLabel + " · " + operation.label,
			Exposures:     []actioncontract.Exposure{actioncontract.ExposurePublic},
			Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
			HTTP: &actioncontract.HTTPBinding{
				Method: operation.method, RouteTemplate: operation.route,
				DisplayRouteTemplate: strings.ReplaceAll(operation.route, "{objectKey}", objectKey),
			},
			NonHTTP: []actioncontract.NonHTTPBinding{{Kind: "runtime_object_action", InvocationKey: key}},
			Permission: &actioncontract.PermissionDefinition{
				Key: key, Owner: owner, ResourceKey: objectKey, OperationKey: operation.key,
				Label: capabilityLabel + " · " + operation.label, Category: capabilityLabel, LifecycleStatus: actioncontract.LifecycleActive,
			},
			EffectClass: operation.effect, RiskLevel: operation.risk,
			IdempotencyDecision: operation.idempotency, AuditClass: "record_authorization", LifecycleStatus: actioncontract.LifecycleActive,
		})
		if err != nil {
			return nil, fmt.Errorf("default object Action %q: %w", key, err)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
