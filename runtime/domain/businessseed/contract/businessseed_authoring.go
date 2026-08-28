package contract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func BusinessSeedAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "seed", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{SeedRecordAuthoringCapability()}}
}

func SeedRecordAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	open := true
	return seedRecordAuthoringCapability(capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}, nil)
}

func SpecializeSeedRecordAuthoringCapability(object definitionmodel.ObjectSchema) capabilitycontract.CapabilityAuthoringDefinition {
	closed := false
	data := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}, Required: []string{}}
	minimal, representative, invalid := map[string]any{}, map[string]any{}, map[string]any{"unknown_field": true}
	for _, field := range object.Fields {
		property := businessSeedFieldSchema(field)
		data.Properties[field.Key] = property
		if field.Required && field.Default == nil && field.DefaultValue == nil {
			data.Required = append(data.Required, field.Key)
			minimal[field.Key] = businessSeedExampleValue(field)
		}
		representative[field.Key] = businessSeedExampleValue(field)
	}
	return seedRecordAuthoringCapability(data, &businessSeedExamples{objectKey: object.Key, minimal: minimal, representative: representative, invalid: invalid})
}

type businessSeedExamples struct {
	objectKey      string
	minimal        map[string]any
	representative map[string]any
	invalid        map[string]any
}

func seedRecordAuthoringCapability(data capabilitycontract.CapabilityAuthoringSchema, selected *businessSeedExamples) capabilitycontract.CapabilityAuthoringDefinition {
	closed := false
	input := &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"seed_key", "object_key", "data"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"seed_key": {Type: "string", MinLength: businessSeedIntPointer(1), MaxLength: businessSeedIntPointer(128)}, "object_key": {Type: "string", MinLength: businessSeedIntPointer(1)}, "data": data, "source_kind": {Type: "string", Default: "builder_v4"}, "source_id": {Type: "string"},
	}}
	output := &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"seed_key", "object_key", "record_id", "data", "source_kind", "source_id", "content_hash", "replayed"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"seed_key": {Type: "string"}, "object_key": {Type: "string"}, "record_id": {Type: "string"}, "data": data, "source_kind": {Type: "string"}, "source_id": {Type: "string"}, "content_hash": {Type: "string"}, "replayed": {Type: "boolean"},
	}}
	examples := []capabilitycontract.CapabilityAuthoringExample{}
	if selected != nil {
		examples = []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"seed_key": selected.objectKey + ".primary", "object_key": selected.objectKey, "data": selected.minimal, "source_kind": "builder_v4", "source_id": "$builder_task_id"}},
			{Name: "representative", Value: map[string]any{"seed_key": selected.objectKey + ".representative", "object_key": selected.objectKey, "data": selected.representative, "source_kind": "builder_v4", "source_id": "$builder_task_id"}},
			{Name: "invalid_with_repair", Value: map[string]any{"seed_key": selected.objectKey + ".invalid", "object_key": selected.objectKey, "data": selected.invalid}, ExpectedErrorCodes: []string{"backend.validation.unknown_field"}},
		}
	}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "seed.record", Status: "supported", Lifecycle: "idempotent_acceptance_evidence", Requires: []string{"schema.object"},
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "seed_key", Type: "seed_key", Required: true}, {Key: "object_key", Type: "object_key", Required: true}, {Key: "data", Type: "object", Required: true}, {Key: "source_kind", Type: "string", Default: "builder_v4"}, {Key: "source_id", Type: "string"}},
		Permissions: []string{"workspace.admin"}, AuditEvents: []string{"business_seed_materialized"}, ValidationEndpoint: "POST /business-seeds/{seedKey}/validate", ConfigurationRoutes: []string{"PUT /business-seeds/{seedKey}", "GET /business-seeds/{seedKey}", "GET /business-seeds/{seedKey}/versions"}, ResourceKeyPathParameter: "seedKey",
		ResourceOperations: &capabilitycontract.CapabilityAuthoringResourceOperations{PersistenceMode: "immutable_resource", Validate: "POST /business-seeds/{seedKey}/validate", Upsert: "PUT /business-seeds/{seedKey}", UpsertHeaders: capabilitycontract.DirectAuthoringUpsertHeaders(), SuccessSchema: capabilitycontract.DirectAuthoringSuccessSchema(), Get: "GET /business-seeds/{seedKey}", Versions: "GET /business-seeds/{seedKey}/versions"},
		InputSchema:        input, OutputSchema: output, OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "record_id", JSONPointer: "/record_id", Type: "record_id", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "object_key", InputJSONPointer: "/object_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/object_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"metadata.schema_snapshot", "business_seed_provenance", "record"}, WriteSet: []string{"record", "business_seed_provenance"}, Transaction: "record_insert_with_provenance_compensation", Idempotency: "seed_key_and_content_hash", SideEffects: []string{"audit:business_seed_materialized"}, SideEffectLevel: "internal", Compensation: "delete_inserted_record_if_provenance_persistence_fails", PermissionModel: "workspace.admin", ChangeControl: "direct_on_configuring_runtime_change_plan_on_existing_runtime"},
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.business_seed.key_required", FieldPath: "seed_key", MessageKey: "backend.business_seed.key_required"}, {Code: "backend.business_seed.key_invalid", FieldPath: "seed_key", MessageKey: "backend.business_seed.key_invalid"}, {Code: "backend.business_seed.not_found", FieldPath: "seed_key", MessageKey: "backend.business_seed.not_found"}, {Code: "backend.business_seed.provenance_orphaned", FieldPath: "seed_key", MessageKey: "backend.business_seed.provenance_orphaned"}, {Code: "backend.business_seed.object_not_found", FieldPath: "object_key", MessageKey: "backend.business_seed.object_not_found"}, {Code: "backend.business_seed.identity_object_forbidden", FieldPath: "object_key", MessageKey: "backend.business_seed.identity_object_forbidden"}, {Code: "backend.business_seed.reference_not_found", FieldPath: "data", MessageKey: "backend.business_seed.reference_not_found"}, {Code: "backend.business_seed.content_conflict", FieldPath: "seed_key", MessageKey: "backend.business_seed.content_conflict"}, {Code: "backend.business_seed.workspace_unsupported", FieldPath: "workspace_id", MessageKey: "backend.business_seed.workspace_unsupported"}, {Code: "backend.business_seed.source_id_required", FieldPath: "source_id", MessageKey: "backend.business_seed.source_id_required"}, {Code: "backend.validation.unknown_field", FieldPath: "data", MessageKey: "backend.validation.unknown_field"}, {Code: "backend.validation.required", FieldPath: "data", MessageKey: "backend.validation.required"}},
		Examples:           examples,
		Sources:            []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/businessseed/model/businessseed_model.go", Symbol: "SeedRecordAuthoringRequest"}, {Kind: "validation", Path: "runtime/application/seed/business/business_seed_authoring_application_service.go", Symbol: "BusinessSeedAuthoringApplicationService.Validate"}, {Kind: "service", Path: "runtime/application/seed/business/business_seed_authoring_application_service.go", Symbol: "BusinessSeedAuthoringApplicationService.Apply"}},
	}
}

func businessSeedFieldSchema(field definitionmodel.FieldSchema) capabilitycontract.CapabilityAuthoringSchema {
	property := capabilitycontract.CapabilityAuthoringSchema{Default: field.Default}
	if property.Default == nil {
		property.Default = field.DefaultValue
	}
	switch field.Type {
	case "integer":
		property.Type = "integer"
	case "number", "percent":
		property.Type = "number"
	case "boolean":
		property.Type = "boolean"
	case "json":
		open := true
		property.Type, property.AdditionalProperties = "object", &open
	default:
		property.Type = "string"
	}
	for _, option := range field.Validation.Options {
		property.Enum = append(property.Enum, option)
	}
	return property
}

func businessSeedExampleValue(field definitionmodel.FieldSchema) any {
	if field.Default != nil {
		return field.Default
	}
	if field.DefaultValue != nil {
		return field.DefaultValue
	}
	switch field.Type {
	case "integer":
		return 1
	case "number", "percent":
		return 1.0
	case "boolean":
		return true
	case "json":
		return map[string]any{}
	case "date":
		return "2026-01-01"
	case "datetime":
		return "2026-01-01T00:00:00Z"
	default:
		return "example_" + field.Key
	}
}

func businessSeedIntPointer(value int) *int { return &value }
