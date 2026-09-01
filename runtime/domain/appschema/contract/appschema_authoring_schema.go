package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

const metadataJSONSchemaDraft = "https://json-schema.org/draft/2020-12/schema"

func metadataAuthoringRequestSchema(payload capabilitycontract.CapabilityAuthoringSchema, objectKeyRequired bool) *capabilitycontract.CapabilityAuthoringSchema {
	required := []string{"payload"}
	if objectKeyRequired {
		required = append(required, "object_key")
	}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: metadataJSONSchemaDraft, Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: required,
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"object_key":  metadataStringSchema("Runtime object owning this resource."),
			"name":        metadataStringSchema("Optional display name stored with the definition."),
			"source_kind": metadataStringSchema("Stable authoring source kind."),
			"source_id":   metadataStringSchema("Stable authoring source identifier."),
			"payload":     payload,
		},
	}
}

func metadataAuthoringOutputSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	definition := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Required: []string{"payload", "resource_key", "resource_type", "schema_hash"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"resource_type": {Type: "string"}, "resource_key": {Type: "string"}, "object_key": {Type: "string"}, "name": {Type: "string"},
			"payload": payload, "schema_version": {Type: "string"}, "schema_hash": {Type: "string"}, "source_kind": {Type: "string"}, "source_id": {Type: "string"},
			"disabled_at": {Type: "string"}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
		},
	}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: metadataJSONSchemaDraft, Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Required: []string{"definition", "resource_hash", "schema", "snapshot_hash"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"definition":    definition,
			"resource_hash": {Type: "string"},
			"snapshot_hash": {Type: "string"},
			// The endpoint currently returns the complete Runtime schema snapshot.
			// It remains explicitly open until P3 introduces the bounded V4 result envelope.
			"schema": {Type: "object", AdditionalProperties: metadataBoolPointer(true)},
		},
	}
}

func metadataObjectPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	capabilities := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"create": {Type: "boolean", Default: true}, "read": {Type: "boolean", Default: true},
			"update": {Type: "boolean", Default: true}, "delete": {Type: "boolean", Default: true},
			"export": {Type: "boolean", Default: true},
		},
	}
	config := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"title_field":  {Type: "string", MinLength: metadataIntPointer(1)},
			"write_policy": {Type: "string", Enum: []any{"direct_crud", "action_only"}, Default: "direct_crud"},
		},
	}
	lifecyclePolicy := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"mode"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"mode":             {Type: "string", Enum: []any{"mutable", "soft_delete_only", "append_only", "immutable_after_state"}},
			"state_field":      {Type: "string"},
			"immutable_states": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
		},
	}
	ledgerPolicy := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"integrity"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"integrity": {Type: "string", Enum: []any{"sha256_chain"}},
			"signature": {Type: "string", Enum: []any{"none", "hmac_sha256"}, Default: "none"},
		},
	}
	exportAssurancePolicy := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"required_methods"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"required_methods":              {Type: "array", MinItems: metadataIntPointer(1), Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: []any{"normal_login", "recent_reauth", "otp", "maker_checker", "workflow_approval"}}},
			"recent_reauth_max_age_seconds": {Type: "integer", Minimum: metadataFloatPointer(1), Maximum: metadataFloatPointer(86400)},
		},
	}
	ux := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"kind": {Type: "string", Enum: []any{"identity_profile_extension"}},
			"config": {Type: "object", AdditionalProperties: metadataBoolPointer(false), Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
				"identity_relation_field": {Type: "string", MinLength: metadataIntPointer(1)},
			}},
			"display": {Type: "object", AdditionalProperties: metadataBoolPointer(false), Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
				"title_field": {Type: "string", MinLength: metadataIntPointer(1)},
			}},
		},
	}
	return capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"key", "name"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key":                     metadataNonEmptyStringSchema("Stable lowercase object key matching the resourceKey path."),
			"name":                    metadataNonEmptyStringSchema("Human-readable object name."),
			"description":             {Type: "string"},
			"capabilities":            capabilities,
			"config":                  config,
			"fields":                  {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: metadataBoolPointer(false)}, Default: []any{}},
			"lifecycle_policy":        lifecyclePolicy,
			"ledger_policy":           ledgerPolicy,
			"export_assurance_policy": exportAssurancePolicy,
			"ux":                      ux,
		},
	}
}

func metadataFieldPayloadSchema(fieldTypes []string) capabilitycontract.CapabilityAuthoringSchema {
	values := make([]any, len(fieldTypes))
	for index, value := range fieldTypes {
		values[index] = value
	}
	config := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false),
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"precision":     {Type: "integer", Minimum: metadataFloatPointer(1), Maximum: metadataFloatPointer(38), Default: 19},
			"scale":         {Type: "integer", Minimum: metadataFloatPointer(0), Maximum: metadataFloatPointer(38), Default: 2},
			"rounding_mode": {Type: "string", Enum: []any{"ceiling", "down", "floor", "half_even", "half_up", "up"}, Default: "half_even"},
			"currency_code": {Type: "string", Format: "iso-4217", Default: "XXX"},
			"target":        {Type: "string"}, "cardinality": {Type: "string", Enum: []any{"many_to_one", "one_to_one"}, Default: "many_to_one"},
			"on_delete": {Type: "string", Enum: []any{"cascade", "restrict", "set_null"}, Default: "restrict"}, "inverse_name": {Type: "string"}, "indexed": {Type: "boolean", Default: true},
		},
	}
	return capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"key", "name", "type"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": metadataNonEmptyStringSchema("Stable field key."), "name": metadataNonEmptyStringSchema("Human-readable field name."), "description": {Type: "string"},
			"type": {Type: "string", Enum: values}, "required": {Type: "boolean", Default: false}, "unique": {Type: "boolean", Default: false},
			"default": {}, "default_value": {}, "config": config,
		},
	}
}

func metadataRelationPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	payload := metadataFieldPayloadSchema([]string{"relation"})
	payload.Required = append(payload.Required, "config")
	config := payload.Properties["config"]
	config.Required = []string{"target"}
	payload.Properties["config"] = config
	return payload
}

func metadataDictionaryItemSchema() capabilitycontract.CapabilityAuthoringSchema {
	return capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"key", "value"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": metadataNonEmptyStringSchema("Stable item key."), "label": {Type: "string"}, "description": {Type: "string"}, "value": metadataNonEmptyStringSchema("Stable stored value."),
			"sort_order": {Type: "integer", Minimum: metadataFloatPointer(0)}, "locale": {Type: "string"}, "status": {Type: "string", Enum: []any{"active", "disabled"}},
			"parent_key": {Type: "string"}, "color": {Type: "string"}, "icon": {Type: "string"}, "tags": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
		},
	}
}

func metadataDictionaryPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	item := metadataDictionaryItemSchema()
	return capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"items", "key"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": metadataNonEmptyStringSchema("Stable dictionary key matching the resourceKey path."), "name": {Type: "string"}, "description": {Type: "string"},
			"items": {Type: "array", Items: &item}, "config": {Type: "object", AdditionalProperties: metadataBoolPointer(false)},
		},
	}
}

func metadataViewPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	stringArray := capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}}
	sortObject := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"field": {Type: "string"}, "direction": {Type: "string", Enum: []any{"asc", "desc"}}}}
	sortItem := capabilitycontract.CapabilityAuthoringSchema{OneOf: []capabilitycontract.CapabilityAuthoringSchema{{Type: "string"}, sortObject}}
	filter := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"key": {Type: "string"}, "field": {Type: "string"}, "source": {Type: "string", Enum: []any{"current_user"}}, "value": {}}}
	config := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: metadataBoolPointer(false), Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"business_view": {Type: "string"}, "columns": stringArray, "end_field": {Type: "string"}, "filters": {Type: "array", Items: &filter}, "group_by": {Type: "string"}, "lane_field": {Type: "string"},
		"page_size": {Type: "integer", Minimum: metadataFloatPointer(1), Maximum: metadataFloatPointer(200), Default: 25}, "route": {Type: "string"}, "search_fields": stringArray,
		"sort": {Type: "array", Items: &sortItem}, "start_field": {Type: "string"}, "title_field": {Type: "string"},
	}}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: metadataBoolPointer(false), Required: []string{"key", "name", "object_key", "type", "config"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": metadataNonEmptyStringSchema("Stable view key matching the resourceKey path."), "name": metadataNonEmptyStringSchema("Human-readable view name."), "object_key": metadataNonEmptyStringSchema("Runtime object queried by the view."), "type": metadataNonEmptyStringSchema("Runtime view presentation hint."),
		"i18n": {Type: "object", AdditionalProperties: metadataBoolPointer(true)}, "config": config,
	}}
}

func metadataAuthoringExecution(resource string) *capabilitycontract.CapabilityAuthoringExecution {
	return &capabilitycontract.CapabilityAuthoringExecution{
		ReadSet:     []string{"metadata.schema_snapshot", resource},
		Transaction: "read_only_candidate_validation", Idempotency: "naturally_idempotent_at_candidate_hash",
		SideEffectLevel: "none", PermissionModel: "runtime.appschema.validate_application_definition", ChangeControl: "source_controlled_json",
	}
}

func metadataConfigurationRoutes(resourceType string) []string {
	base := "/tenant-admin/metadata/definitions/" + resourceType + "/{resourceKey}"
	return []string{
		"GET " + base,
		"POST " + base + "/validate",
		"GET /domain-system-snapshot",
		"GET /domain-reference-graph",
	}
}

func metadataResourceOperations(resourceType string) *capabilitycontract.CapabilityAuthoringResourceOperations {
	// Definition writes are never exposed as single-resource operations. The
	// payload schema remains on the capability, while publication always uses
	// the reviewed workspace system-draft lifecycle in ConfigurationRoutes.
	return nil
}

func VersionedApplicationDefinitionRequestSchema(payload capabilitycontract.CapabilityAuthoringSchema, objectKeyRequired bool) *capabilitycontract.CapabilityAuthoringSchema {
	return metadataAuthoringRequestSchema(payload, objectKeyRequired)
}

func VersionedApplicationDefinitionOutputSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	return metadataAuthoringOutputSchema(payload)
}

func VersionedApplicationDefinitionRoutes(resourceType string) []string {
	return metadataConfigurationRoutes(resourceType)
}

func VersionedApplicationDefinitionOperations(resourceType string) *capabilitycontract.CapabilityAuthoringResourceOperations {
	return metadataResourceOperations(resourceType)
}

func VersionedApplicationDefinitionExecution(resource string) *capabilitycontract.CapabilityAuthoringExecution {
	return metadataAuthoringExecution(resource)
}

func metadataObjectReference(pointer string) capabilitycontract.CapabilityAuthoringReference {
	return capabilitycontract.CapabilityAuthoringReference{Kind: "object_key", InputJSONPointer: pointer, ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/object_key"}
}

func metadataRelationTargetReference(pointer string) capabilitycontract.CapabilityAuthoringReference {
	return capabilitycontract.CapabilityAuthoringReference{Kind: "relation_target_object_key", InputJSONPointer: pointer, ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/relation_target_object_key"}
}

func metadataStringSchema(description string) capabilitycontract.CapabilityAuthoringSchema {
	return capabilitycontract.CapabilityAuthoringSchema{Type: "string", Description: description}
}

func metadataNonEmptyStringSchema(description string) capabilitycontract.CapabilityAuthoringSchema {
	return capabilitycontract.CapabilityAuthoringSchema{Type: "string", MinLength: metadataIntPointer(1), Description: description}
}

func metadataBoolPointer(value bool) *bool { return &value }
