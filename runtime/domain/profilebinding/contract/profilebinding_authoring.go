package contract

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func ProfileBindingAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	// The payload is an Object's `ux.config` plus the object key it hangs on, so
	// it is built from the one schema both surfaces publish; see
	// appschemacontract.IdentityProfileExtensionConfigSchema.
	payload := appschemacontract.IdentityProfileExtensionConfigSchema()
	properties := make(map[string]capabilitycontract.CapabilityAuthoringSchema, len(payload.Properties)+1)
	for key, value := range payload.Properties {
		properties[key] = value
	}
	properties["object_key"] = capabilitycontract.CapabilityAuthoringSchema{Type: "string", MinLength: profileBindingIntPointer(1)}
	payload.Properties = properties
	payload.Required = append([]string{"object_key"}, payload.Required...)
	execution := appschemacontract.VersionedApplicationDefinitionExecution("principal.profile_binding")
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "principal.profile_binding", Status: "supported", Lifecycle: "versioned_metadata", Requires: []string{"schema.object", "schema.relation"},
		SystemDraftResourceType: "identity_profile_binding",
		Parameters:              []capabilitycontract.CapabilityAuthoringParameter{{Key: "object_key", Type: "object_key", Required: true}, {Key: "identity_relation_field", Type: "field_key", Required: true}, {Key: "business_identity", Type: "object", Required: true}, {Key: "expected_schema_hash", Type: "schema_hash", Required: true}},
		Permissions:             []string{"runtime.appschema.validate_application_definition"}, AuditEvents: []string{"metadata_definition_upserted"},
		ValidationEndpoint: "POST /application-schema/definitions/identity_profile_binding/{resourceKey}/validate", ConfigurationRoutes: appschemacontract.VersionedApplicationDefinitionRoutes("identity_profile_binding"), ResourceKeyPathParameter: "resourceKey",
		ResourceOperations: appschemacontract.VersionedApplicationDefinitionOperations("identity_profile_binding"), InputSchema: appschemacontract.VersionedApplicationDefinitionRequestSchema(payload, false), OutputSchema: appschemacontract.VersionedApplicationDefinitionOutputSchema(payload),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "resource_key", JSONPointer: "/definition/resource_key", Type: "identity_profile_binding_key", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "object_key", InputJSONPointer: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/object_key"}, {Kind: "field_key", InputJSONPointer: "/payload/identity_relation_field", ScopeFrom: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/field_key"}},
		Execution:          execution,
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.identity.profile_binding_invalid", FieldPath: "payload", ParameterKeys: []string{"diagnostic"}, MessageKey: "backend.identity.profile_binding_invalid"}},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"object_key": "staff_profile", "identity_relation_field": "identity_user", "business_identity": map[string]any{"key": "staff"}, "default_visibility": "when_readable"}}},
			{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"object_key": "staff_profile", "identity_relation_field": "identity_user", "business_identity": map[string]any{"key": "staff", "status_field": "status", "active_status_values": []any{"active"}, "claims": []any{map[string]any{"claim_key": "territory_id", "field_key": "territory_id"}}}, "summary_fields": []any{"display_name"}, "default_visibility": "when_readable"}}},
			{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"object_key": "missing"}}, ExpectedErrorCodes: []string{"backend.identity.profile_binding_invalid"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "contract", Path: "runtime/domain/profilebinding/contract/profilebinding_authoring.go", Symbol: "ProfileBindingAuthoringCapability"}, {Kind: "model", Path: "runtime/domain/profilebinding/model/profilebinding_binding.go", Symbol: "Binding"}, {Kind: "validation", Path: "runtime/domain/manifest/validation/manifest_validator.go", Symbol: "ValidateIdentityProfileBindings"}, {Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionPayload"}},
	}
}

func profileBindingBoolPointer(value bool) *bool { return &value }
func profileBindingIntPointer(value int) *int    { return &value }
