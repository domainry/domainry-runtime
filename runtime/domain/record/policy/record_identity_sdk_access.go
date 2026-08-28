package policy

import (
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// RecordAllowsObjectAction is the deployment-neutral object authorization
// boundary. Externally authenticated principals are decided exclusively by
// their SDK AccessBundle. Only an explicitly scoped Runtime system principal
// may use process-owned SystemCapabilities.
func RecordAllowsObjectAction(principal principalmodel.Principal, objectKey, action string) bool {
	if allowed, handled := RecordSDKAllowsObjectAction(principal, objectKey, action); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(objectKey, action)
}

func RecordDataScopeForPrincipal(principal principalmodel.Principal, objectKey, action string) string {
	if principal.AccessBundle != nil {
		return "identity_policy"
	}
	if principal.SystemScope.Valid() && principal.Allows(objectKey, action) {
		return "all_records"
	}
	return ""
}

func RecordCanReadFieldForPrincipal(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if allowed, _, handled := RecordSDKReadableField(principal, objectKey, fieldKey); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(objectKey, "read")
}

func RecordCanWriteFieldForPrincipal(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if allowed, _, handled := RecordSDKWritableField(principal, objectKey, fieldKey); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(objectKey, "update")
}

func RecordCanReadObjectFieldForPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema) bool {
	if allowed, _, handled := RecordSDKReadableField(principal, object.Key, field.Key); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "read")
}

func RecordCanWriteObjectFieldForPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema) bool {
	if allowed, _, handled := RecordSDKWritableField(principal, object.Key, field.Key); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "update")
}

func RecordCanExportObjectFieldForPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema) bool {
	if allowed, _, handled := RecordSDKExportableField(principal, object.Key, field.Key); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "export")
}

func RecordCanExportFieldForPrincipal(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if allowed, _, handled := RecordSDKExportableField(principal, objectKey, fieldKey); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(objectKey, "export")
}

func RecordFieldReadMaskedForPrincipal(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if allowed, masked, handled := RecordSDKReadableField(principal, objectKey, fieldKey); handled {
		return allowed && masked
	}
	return false
}

func RecordFieldExportMaskedForPrincipal(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if allowed, masked, handled := RecordSDKExportableField(principal, objectKey, fieldKey); handled {
		return allowed && masked
	}
	return false
}

func RecordFieldRequiresPolicyEvaluation(principal principalmodel.Principal, objectKey, fieldKey, action string) bool {
	if principal.AccessBundle == nil {
		return false
	}
	return identityevaluator.FieldRequiresPolicyEvaluation(*principal.AccessBundle, identityevaluator.FieldRequest{
		Resource: identitysdk.ResourceType(strings.TrimSpace(objectKey)),
		Field:    strings.TrimSpace(fieldKey),
		Action:   identitysdk.Action(strings.TrimSpace(action)),
	})
}

// RecordSDKAllowsObjectAction checks the function and data-policy envelope for
// an object action. Record predicates are evaluated later, once record facts
// are available. handled is false for Runtime-owned system principals and
// explicitly scoped Runtime system principals, which are handled separately.
func RecordSDKAllowsObjectAction(principal principalmodel.Principal, objectKey, action string) (allowed, handled bool) {
	if principal.AccessBundle == nil {
		return false, false
	}
	filter, err := identityevaluator.CompileRecordFilter(
		*principal.AccessBundle,
		identitysdk.ResourceType(strings.TrimSpace(objectKey)),
		identitysdk.Action(normalizeSDKRecordAction(action)),
		time.Now().UTC(),
	)
	if err != nil {
		return false, true
	}
	return len(filter.Allow) > 0, true
}

// RecordSDKAllowsRecord evaluates the SDK's exact action against one record.
// It is deliberately the only policy engine for SDK-authenticated requests.
func RecordSDKAllowsRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, action string, record recordmodel.Record) (allowed, handled bool) {
	if principal.AccessBundle == nil {
		return false, false
	}
	decision, err := identityevaluator.EvaluateWithContext(
		*principal.AccessBundle,
		identitysdk.AccessRequest{
			ObjectKey: object.Key,
			Action:    normalizeSDKRecordAction(action),
			RecordID:  record.ID,
		},
		recordSDKResourceFacts(object, record),
		RecordSDKEvaluationContext(principal),
		time.Now().UTC(),
	)
	return err == nil && decision.Allowed, true
}

func RecordSDKReadableField(principal principalmodel.Principal, objectKey, fieldKey string) (allowed, masked, handled bool) {
	return recordSDKFieldAccess(principal, objectKey, fieldKey, "read")
}

func RecordSDKWritableField(principal principalmodel.Principal, objectKey, fieldKey string) (allowed, masked, handled bool) {
	return recordSDKFieldAccess(principal, objectKey, fieldKey, "update")
}

func RecordSDKExportableField(principal principalmodel.Principal, objectKey, fieldKey string) (allowed, masked, handled bool) {
	if principal.AccessBundle == nil {
		return false, false, false
	}
	resource := identitysdk.ResourceType(strings.TrimSpace(objectKey))
	allowedFields := identityevaluator.ExportableFields(*principal.AccessBundle, resource)
	if !containsSDKField(allowedFields, fieldKey) {
		return false, false, true
	}
	fieldAllowed, fieldMasked, _ := recordSDKFieldAccess(principal, objectKey, fieldKey, "export")
	if !fieldAllowed {
		request := identityevaluator.FieldRequest{Resource: resource, Field: strings.TrimSpace(fieldKey), Action: "export"}
		if identityevaluator.FieldRequiresPolicyEvaluation(*principal.AccessBundle, request) {
			// The SDK has declared a record-dependent field policy. Keep the
			// field in the internal export projection so the record-aware policy
			// service can decide allow/mask/hide for every row.
			return true, false, true
		}
		return false, false, true
	}
	allowed = true
	return allowed, allowed && fieldMasked, true
}

func recordSDKFieldAccess(principal principalmodel.Principal, objectKey, fieldKey, action string) (allowed, masked, handled bool) {
	if principal.AccessBundle == nil {
		return false, false, false
	}
	decision, err := identityevaluator.EvaluateField(*principal.AccessBundle, identityevaluator.FieldRequest{
		Resource: identitysdk.ResourceType(strings.TrimSpace(objectKey)),
		Field:    strings.TrimSpace(fieldKey),
		Action:   identitysdk.Action(strings.ToLower(strings.TrimSpace(action))),
	}, nil)
	if err != nil {
		// A contextual predicate cannot be evaluated without record facts. The
		// coarse field envelope must fail closed; record-aware services call the
		// same SDK evaluator with a Runtime-supplied predicate matcher.
		return false, false, true
	}
	switch decision.Effect {
	case identitysdk.FieldEffectAllow:
		return true, false, true
	case identitysdk.FieldEffectMask:
		return true, true, true
	default:
		return false, false, true
	}
}

func recordSDKResourceFacts(object definitionmodel.ObjectSchema, record recordmodel.Record) identitysdk.ResourceFacts {
	facts := make(identitysdk.ResourceFacts, len(record.Data)+8)
	for key, value := range record.Data {
		facts[key] = value
	}
	// `id exists` is the portable SDK predicate for an unrestricted record
	// scope. Candidate creates do not have a persisted ID yet, but the record
	// fact itself still exists; preserve the key with an empty value so the
	// predicate remains unconditional without fabricating an identifier.
	facts["id"] = record.ID
	copySDKCanonicalFact(facts, "owner_id", RecordOwnerFieldKey(object))
	copySDKCanonicalFact(facts, "department_id", RecordOwnerDepartmentIDFieldKey(object))
	copySDKCanonicalFact(facts, "department_path", RecordOwnerDepartmentPathFieldKey(object))
	copySDKCanonicalFact(facts, "team_id", RecordTeamFieldKey(object))
	copySDKCanonicalFact(facts, "store_id", RecordStoreFieldKey(object))
	copySDKCanonicalFact(facts, "territory_id", RecordTerritoryFieldKey(object))
	copySDKCanonicalFact(facts, "warehouse_id", RecordWarehouseFieldKey(object))
	return facts
}

func copySDKCanonicalFact(facts identitysdk.ResourceFacts, canonical, source string) {
	if strings.TrimSpace(source) == "" {
		return
	}
	if value, exists := facts[source]; exists {
		facts[canonical] = value
	}
}

func normalizeSDKRecordAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "view", "list", "search":
		return "read"
	case "edit":
		return "update"
	default:
		return strings.TrimSpace(action)
	}
}

func containsSDKField(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, value := range values {
		if strings.TrimSpace(value) == expected || strings.TrimSpace(value) == "*" {
			return true
		}
	}
	return false
}
