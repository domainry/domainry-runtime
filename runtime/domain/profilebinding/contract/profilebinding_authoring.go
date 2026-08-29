package contract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	metadatacontract "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
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
	directory := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"enabled": {Type: "boolean"}, "label": {Type: "string"}, "plural_label": {Type: "string"},
		"summary_fields": strings, "filter_fields": strings, "status_field": {Type: "string"}, "action_keys": strings,
	}}
	businessIdentity := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"key", "surface_keys"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: profileBindingIntPointer(1)}, "surface_keys": strings, "status_field": {Type: "string"}, "active_status_values": strings,
		"blacklist_field": {Type: "string"}, "claims": {Type: "array", Items: &claim},
	}}
	payload := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed,
		Required: []string{"contract_version", "min_reader_version", "object_key", "identity_relation_field", "cardinality", "business_identity", "default_visibility"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"contract_version": {Type: "string", Const: "identity-profile-extension"}, "min_reader_version": {Type: "string", Const: "identity-profile-extension-reader"},
			"object_key": {Type: "string", MinLength: profileBindingIntPointer(1)}, "identity_relation_field": {Type: "string", MinLength: profileBindingIntPointer(1)},
			"cardinality": {Type: "string", Enum: []any{"one_to_one"}}, "business_identity": businessIdentity,
			"binding_lifecycle": bindingLifecycle,
			"directory":         directory,
			"summary_fields":    strings, "profile_tabs": strings, "profile_tab_labels": {Type: "object", AdditionalProperties: open},
			"profile_tab_fields": {Type: "object", AdditionalProperties: open}, "profile_tab_related_objects": {Type: "object", AdditionalProperties: open},
			"profile_tab_components": {Type: "object", AdditionalProperties: open}, "default_visibility": {Type: "string", Enum: []any{"when_readable", "hidden"}},
			"required_permissions": strings, "standalone_workspace": {Type: "boolean"}, "provenance": {Type: "object", AdditionalProperties: open},
		},
	}
	execution := metadatacontract.VersionedMetadataDefinitionExecution("principal.profile_binding")
	execution.PermissionModel = "identity.profile_binding.manage"
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "principal.profile_binding", Status: "supported", Lifecycle: "versioned_metadata", Requires: []string{"schema.object", "schema.relation"},
		SystemDraftResourceType: "identity_profile_binding",
		Parameters:              []capabilitycontract.CapabilityAuthoringParameter{{Key: "object_key", Type: "object_key", Required: true}, {Key: "identity_relation_field", Type: "field_key", Required: true}, {Key: "business_identity", Type: "object", Required: true}, {Key: "expected_schema_hash", Type: "schema_hash", Required: true}},
		Permissions:             []string{"identity.profile_binding.manage"}, AuditEvents: []string{"metadata_definition_upserted"},
		ValidationEndpoint: "POST /metadata/definitions/identity_profile_binding/{resourceKey}/validate", ConfigurationRoutes: metadatacontract.VersionedMetadataDefinitionRoutes("identity_profile_binding"), ResourceKeyPathParameter: "resourceKey",
		ResourceOperations: metadatacontract.VersionedMetadataDefinitionOperations("identity_profile_binding"), FrontendSupportKey: "identity.profile-binding.v1", MinimumFrontendVersion: "0.1.0",
		InputSchema: metadatacontract.VersionedMetadataDefinitionRequestSchema(payload, false), OutputSchema: metadatacontract.VersionedMetadataDefinitionOutputSchema(payload),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "resource_key", JSONPointer: "/definition/resource_key", Type: "identity_profile_binding_key", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "object_key", InputJSONPointer: "/payload/object_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/object_key"}, {Kind: "field_key", InputJSONPointer: "/payload/identity_relation_field", ScopeFrom: "/payload/object_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/field_key"}},
		Execution:          execution,
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.identity.profile_binding_invalid", FieldPath: "payload", ParameterKeys: []string{"diagnostic"}, MessageKey: "backend.identity.profile_binding_invalid"}},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"contract_version": "identity-profile-extension", "min_reader_version": "identity-profile-extension-reader", "object_key": "staff_profile", "identity_relation_field": "identity_user", "cardinality": "one_to_one", "business_identity": map[string]any{"key": "staff", "surface_keys": []any{"admin"}}, "default_visibility": "when_readable"}}},
			{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"contract_version": "identity-profile-extension", "min_reader_version": "identity-profile-extension-reader", "object_key": "staff_profile", "identity_relation_field": "identity_user", "cardinality": "one_to_one", "business_identity": map[string]any{"key": "staff", "surface_keys": []any{"admin"}, "status_field": "status", "active_status_values": []any{"active"}, "claims": []any{map[string]any{"claim_key": "territory_id", "field_key": "territory_id"}}}, "summary_fields": []any{"display_name"}, "default_visibility": "when_readable"}}},
			{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"object_key": "missing"}}, ExpectedErrorCodes: []string{"backend.identity.profile_binding_invalid"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "contract", Path: "runtime/domain/profilebinding/contract/profilebinding_authoring.go", Symbol: "ProfileBindingAuthoringCapability"}, {Kind: "model", Path: "runtime/domain/profilebinding/model/profilebinding_binding.go", Symbol: "Binding"}, {Kind: "validation", Path: "runtime/domain/manifest/validation/manifest_validator.go", Symbol: "ValidateIdentityProfileBindings"}, {Kind: "service", Path: "runtime/application/metadata/metadata_definition_orchestration_application_service.go", Symbol: "ApplicationSchemaService.UpsertMetadataDefinition"}},
	}
}

func profileBindingBoolPointer(value bool) *bool { return &value }
func profileBindingIntPointer(value int) *int    { return &value }
