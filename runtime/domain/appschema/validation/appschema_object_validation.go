package validation

import (
	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ApplicationSchemaValidateObjectDefinition validates the independently authorable
// object shell. Fields are intentionally authored through schema.field so the
// object creation contract stays small and repairable.
func ApplicationSchemaValidateObjectDefinition(resourceKey string, payload json.RawMessage) (json.RawMessage, error) {
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(payload, &declared); err != nil {
		return nil, badRequest("backend.metadata.object_definition_invalid")
	}
	var object definitionmodel.ObjectSchema
	if err := decodeClosedAuthoringJSON(payload, &object); err != nil {
		return nil, badRequest("backend.metadata.object_definition_invalid")
	}
	resourceKey = strings.TrimSpace(resourceKey)
	object.Key = strings.TrimSpace(object.Key)
	if object.Key == "" && declared["key"] == nil {
		object.Key = resourceKey
	}
	object.Name = strings.TrimSpace(object.Name)
	if object.Key == "" || object.Key != resourceKey {
		return nil, badRequest("backend.metadata.object_key_mismatch", "object", resourceKey)
	}
	if object.Name == "" {
		return nil, badRequest("backend.metadata.object_name_required", "object", resourceKey)
	}
	capabilities := definitionmodel.EffectiveObjectCapabilities(object)
	object.Capabilities = &capabilities
	if len(object.Fields) > 0 || len(object.Validations) > 0 {
		return nil, badRequest("backend.metadata.object_shell_only", "object", resourceKey)
	}
	if raw, exists := object.Config["write_policy"]; exists {
		policy, ok := raw.(string)
		policy = strings.TrimSpace(policy)
		if !ok || policy != "direct_crud" && policy != "action_only" {
			return nil, badRequest("backend.metadata.object_write_policy_invalid", "object", resourceKey)
		}
		object.Config["write_policy"] = policy
	}
	if object.LifecyclePolicy != nil {
		object.LifecyclePolicy.Mode = strings.TrimSpace(object.LifecyclePolicy.Mode)
		object.LifecyclePolicy.StateField = strings.TrimSpace(object.LifecyclePolicy.StateField)
		seenStates := map[string]bool{}
		states := make([]string, 0, len(object.LifecyclePolicy.ImmutableStates))
		for _, state := range object.LifecyclePolicy.ImmutableStates {
			state = strings.TrimSpace(state)
			if state == "" || seenStates[state] {
				return nil, badRequest("backend.metadata.object_lifecycle_policy_invalid", "object", resourceKey)
			}
			seenStates[state] = true
			states = append(states, state)
		}
		object.LifecyclePolicy.ImmutableStates = states
		switch object.LifecyclePolicy.Mode {
		case definitionmodel.ObjectLifecycleMutable, definitionmodel.ObjectLifecycleSoftDeleteOnly, definitionmodel.ObjectLifecycleAppendOnly:
			if object.LifecyclePolicy.StateField != "" || len(states) > 0 {
				return nil, badRequest("backend.metadata.object_lifecycle_policy_invalid", "object", resourceKey)
			}
		case definitionmodel.ObjectLifecycleImmutableAfterState:
			if object.LifecyclePolicy.StateField == "" || len(states) == 0 {
				return nil, badRequest("backend.metadata.object_lifecycle_policy_invalid", "object", resourceKey)
			}
		default:
			return nil, badRequest("backend.metadata.object_lifecycle_policy_invalid", "object", resourceKey)
		}
		capabilities = definitionmodel.EffectiveObjectCapabilities(object)
		object.Capabilities = &capabilities
	}
	if object.LedgerPolicy != nil {
		object.LedgerPolicy.Integrity = strings.TrimSpace(object.LedgerPolicy.Integrity)
		if object.LedgerPolicy.Integrity == "" {
			object.LedgerPolicy.Integrity = definitionmodel.ObjectLedgerIntegritySHA256Chain
		}
		object.LedgerPolicy.Signature = strings.TrimSpace(object.LedgerPolicy.Signature)
		if object.LedgerPolicy.Signature == "" {
			object.LedgerPolicy.Signature = definitionmodel.ObjectLedgerSignatureNone
		}
		if object.LedgerPolicy.Integrity != definitionmodel.ObjectLedgerIntegritySHA256Chain || object.LifecyclePolicy == nil || object.LifecyclePolicy.Mode != definitionmodel.ObjectLifecycleAppendOnly {
			return nil, badRequest("backend.metadata.object_ledger_policy_invalid", "object", resourceKey)
		}
		switch object.LedgerPolicy.Signature {
		case "", definitionmodel.ObjectLedgerSignatureNone, definitionmodel.ObjectLedgerSignatureHMACSHA256:
		default:
			return nil, badRequest("backend.metadata.object_ledger_policy_invalid", "object", resourceKey)
		}
	}
	if object.ExportAssurancePolicy != nil {
		policy := object.ExportAssurancePolicy
		seen := map[string]bool{}
		methods := make([]string, 0, len(policy.RequiredMethods))
		for _, method := range policy.RequiredMethods {
			method = strings.TrimSpace(method)
			if !metadataObjectAssuranceMethod(method) || seen[method] {
				return nil, badRequest("backend.metadata.object_export_assurance_policy_invalid", "object", resourceKey)
			}
			seen[method], methods = true, append(methods, method)
		}
		policy.RequiredMethods = methods
		if len(methods) == 0 || seen[definitionmodel.ActionAssuranceRecentReauth] && (policy.RecentReauthMaxAgeSeconds < 1 || policy.RecentReauthMaxAgeSeconds > 86400) || !seen[definitionmodel.ActionAssuranceRecentReauth] && policy.RecentReauthMaxAgeSeconds != 0 {
			return nil, badRequest("backend.metadata.object_export_assurance_policy_invalid", "object", resourceKey)
		}
		if strings.TrimSpace(policy.ApprovalVersionField) != "" || strings.TrimSpace(policy.ApprovalHashField) != "" || strings.TrimSpace(policy.MakerField) != "" {
			return nil, badRequest("backend.metadata.object_export_assurance_policy_invalid", "object", resourceKey)
		}
	}
	object.Fields = []definitionmodel.FieldSchema{}
	// object was decoded from JSON and normalization only replaces values with
	// JSON-safe schema primitives, so encoding the normalized shell cannot fail.
	normalized, _ := json.Marshal(object)
	return normalized, nil
}

func metadataObjectAssuranceMethod(method string) bool {
	switch method {
	case definitionmodel.ActionAssuranceNormalLogin, definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP, definitionmodel.ActionAssuranceMakerChecker, definitionmodel.ActionAssuranceWorkflowApproval:
		return true
	default:
		return false
	}
}
