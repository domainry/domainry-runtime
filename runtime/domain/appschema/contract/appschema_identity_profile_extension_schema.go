package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

// IdentityProfileExtensionConfigSchema is the authoring shape of an Object's
// `ux.config` when `ux.kind` is `identity_profile_extension`, and the same shape
// the `principal.profile_binding` capability takes as its payload once the
// object key is added.
//
// Both surfaces decode into profilebinding model.Binding, so they must publish
// one schema: an author reads the `schema.object` contract, writes exactly the
// keys it declares, and a closed object that names only part of the binding then
// rejects a correct model at plan time with a message about a key the contract
// never mentioned. The profile binding package builds its payload from this
// function rather than repeating the properties.
func IdentityProfileExtensionConfigSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := metadataBoolPointer(false), metadataBoolPointer(true)
	nonEmpty := metadataIntPointer(1)
	strings := capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", MinLength: nonEmpty}}
	claim := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"claim_key", "field_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"claim_key": {Type: "string", MinLength: nonEmpty}, "field_key": {Type: "string", MinLength: nonEmpty},
	}}
	claimProof := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Required: []string{"type", "field_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"type": {Type: "string", Enum: []any{"email", "phone", "external_idp_subject"}}, "field_key": {Type: "string", MinLength: nonEmpty},
	}}
	bindingLifecycle := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"allow_unbound": {Type: "boolean"}, "invitation_channels": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: []any{"email", "sms", "external_idp"}}},
		"claim_proofs": {Type: "array", Items: &claimProof}, "rebind_requires_approval": {Type: "boolean"}, "rebind_revokes_sessions": {Type: "boolean"},
	}}
	businessIdentity := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: closed, Required: []string{"key"},
		Description: "Stable business identity of the profile. An identity-delivery Action's binding_key must equal this key.",
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": {Type: "string", MinLength: nonEmpty}, "status_field": {Type: "string"}, "active_status_values": strings,
			"blacklist_field": {Type: "string"}, "claims": {Type: "array", Items: &claim},
		},
	}
	return capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: closed,
		Required: []string{"identity_relation_field", "business_identity", "default_visibility"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"identity_relation_field": {Type: "string", MinLength: nonEmpty, Description: "Unique, nullable relation field on this Object targeting identity_user."},
			"cardinality":             {Type: "string", Enum: []any{"one_to_one"}, Description: "Only one_to_one is accepted."},
			"business_identity":       businessIdentity,
			"binding_lifecycle":       bindingLifecycle,
			"summary_fields":          strings, "profile_tabs": strings,
			"profile_tab_labels":          {Type: "object", AdditionalProperties: open},
			"profile_tab_fields":          {Type: "object", AdditionalProperties: open},
			"profile_tab_related_objects": {Type: "object", AdditionalProperties: open},
			"profile_tab_components":      {Type: "object", AdditionalProperties: open},
			"default_visibility":          {Type: "string", Enum: []any{"when_readable", "hidden"}},
			"required_permissions":        strings,
			"standalone_workspace":        {Type: "boolean"},
			"provenance":                  {Type: "object", AdditionalProperties: open},
		},
	}
}
