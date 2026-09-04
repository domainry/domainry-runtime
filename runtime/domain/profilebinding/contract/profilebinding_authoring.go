package contract

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func ProfileBindingAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	closed, open := profileBindingBoolPointer(false), profileBindingBoolPointer(true)
	strings := capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", MinLength: profileBindingIntPointer(1)}}
	claim := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"claim_key", "field_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"claim_key": {Type: "string", MinLength: profileBindingIntPointer(1)}, "field_key": {Type: "string", MinLength: profileBindingIntPointer(1)},
	}}
	claimProof := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"type", "field_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"type": {Type: "string", Enum: []any{"email", "phone", "external_idp_subject"}}, "field_key": {Type: "string", MinLength: profileBindingIntPointer(1)},
	}}
	bindingLifecycle := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"allow_unbound": {Type: "boolean"}, "invitation_channels": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: []any{"email", "sms", "external_idp"}}},
		"claim_proofs": {Type: "array", Items: &claimProof}, "rebind_requires_approval": {Type: "boolean"}, "rebind_revokes_sessions": {Type: "boolean"},
	}}
	businessIdentity := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: profileBindingIntPointer(1)}, "status_field": {Type: "string"}, "active_status_values": strings,
		"blacklist_field": {Type: "string"}, "claims": {Type: "array", Items: &claim},
	}}
	payload := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed,
		Required: []string{"object_key", "identity_relation_field", "business_identity", "default_visibility"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"object_key": {Type: "string", MinLength: profileBindingIntPointer(1)}, "identity_relation_field": {Type: "string", MinLength: profileBindingIntPointer(1)},
			"business_identity": businessIdentity,
			"binding_lifecycle": bindingLifecycle,
			"summary_fields":    strings, "profile_tabs": strings, "profile_tab_labels": {Type: "object", AdditionalProperties: open},
			"profile_tab_fields": {Type: "object", AdditionalProperties: open}, "profile_tab_related_objects": {Type: "object", AdditionalProperties: open},
			"profile_tab_components": {Type: "object", AdditionalProperties: open}, "default_visibility": {Type: "string", Enum: []any{"when_readable", "hidden"}},
			"required_permissions": strings, "standalone_workspace": {Type: "boolean"}, "provenance": {Type: "object", AdditionalProperties: open},
		},
	}
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
