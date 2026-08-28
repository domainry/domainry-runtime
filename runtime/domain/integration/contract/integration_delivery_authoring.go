package integrationcontract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{
		integrationCatalogAuthoringCapability(),
		IntegrationConnectorDefinitionAuthoringCapability(),
		IntegrationConnectorOperationAuthoringCapability(),
		IntegrationBindingValidationAuthoringCapability(),
		IntegrationConnectionAuthoringCapability(),
		IntegrationConnectionDisableAuthoringCapability(),
		IntegrationConnectionRotateAuthoringCapability(),
		IntegrationConnectionDeleteAuthoringCapability(),
		IntegrationOperationTestAuthoringCapability(),
	}
	capabilities = append(capabilities, IntegrationDeliveryAuthoringCapabilities()...)
	return capabilitycontract.CapabilityAuthoringDomain{Key: "integration", Capabilities: capabilities}
}

func integrationCatalogAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	closed := false
	connector := integrationConnectorSchema()
	connection := *integrationConnectionOutputSchema()
	connection.Schema = ""
	output := &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"contract_version", "contract_hash", "connectors", "connections", "connections_available", "count"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"contract_version": {Type: "string"}, "contract_hash": {Type: "string"}, "connectors": {Type: "array", Items: &connector}, "connections": {Type: "array", Items: &connection}, "connections_available": {Type: "boolean"}, "count": {Type: "integer"}}}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.catalog", Status: "supported", Lifecycle: "runtime_builtin_read_only", Permissions: []string{"integration.catalog.view"},
		ConfigurationRoutes: []string{"GET /integrations/connectors"}, FrontendSupportKey: "integration.catalog.v1",
		Execution:    &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connector_definition"}, Transaction: "read_only", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "integration.catalog.view"},
		InputSchema:  &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}},
		OutputSchema: output, OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "connector_contract_hash", JSONPointer: "/contract_hash", Type: "contract_hash", VisibleTo: "subsequent_capability_calls"}, {Name: "connectors", JSONPointer: "/connectors", Type: "connector_list", VisibleTo: "subsequent_capability_calls"}},
		Examples: []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{}}, {Name: "representative", Value: map[string]any{}}},
		Sources:  []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/application/integration/integration_application_catalog.go", Symbol: "IntegrationApplicationService.IntegrationConnectorCatalog"}},
	}
}

func IntegrationDeliveryAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	return []capabilitycontract.CapabilityAuthoringDefinition{
		integrationInvocationListAuthoringCapability(),
		integrationEventListAuthoringCapability(),
		integrationOfflineEventRecoveryAuthoringCapability(),
		integrationOutboxListAuthoringCapability(),
		integrationOutboxEnqueueAuthoringCapability(),
		integrationOutboxStatusAuthoringCapability(),
		integrationOutboxRetryAuthoringCapability(),
	}
}

func integrationOfflineEventRecoveryAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	closed, open := false, true
	minimum, maximum := 1, 500
	eventInput := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"provider", "event_type", "external_id"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"provider": {Type: "string"}, "event_type": {Type: "string"}, "external_id": {Type: "string"}, "payload": {Type: "object", AdditionalProperties: &open},
	}}
	input := &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"events"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"events": {Type: "array", Items: &eventInput, MinItems: &minimum, MaxItems: &maximum},
	}}
	eventOutput := integrationEventSchema()
	output := &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"accepted", "duplicates", "reconciliation_required", "events"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"accepted": {Type: "integer"}, "duplicates": {Type: "integer"}, "reconciliation_required": {Type: "integer"},
		"events": {Type: "array", Items: &eventOutput}, "conflict_external_ids": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
	}}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.event.recover_offline", Status: "supported", Lifecycle: "offline_event_recovery", Permissions: []string{"integration.invoke"},
		ConfigurationRoutes: []string{"POST /integrations/events/recover-offline"}, FrontendSupportKey: "integration.activity-recovery.v1",
		InputSchema: input, OutputSchema: output,
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "accepted", JSONPointer: "/accepted", Type: "integer", VisibleTo: "subsequent_capability_calls"}, {Name: "reconciliation_required", JSONPointer: "/reconciliation_required", Type: "integer", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.event"}, WriteSet: []string{"integration.event", "integration.event_mapping_intent"}, Transaction: "per_event_acceptance_transaction", Idempotency: "provider_external_event_id_and_content_fingerprint", SideEffects: []string{"integration_event_received", "integration_event_duplicate", "integration_event_reconciliation_required"}, SideEffectLevel: "internal", PermissionModel: "integration.invoke"},
		Errors:          []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.offline_recovery.batch_size_invalid", FieldPath: "events", MessageKey: "backend.integration.offline_recovery.batch_size_invalid"}, {Code: "backend.integration.event.external_id_conflict", FieldPath: "events[].external_id", MessageKey: "backend.integration.event.external_id_conflict"}},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"events": []any{map[string]any{"provider": "edge_adapter", "event_type": "fact.created", "external_id": "event-1"}}}},
			{Name: "representative", Value: map[string]any{"events": []any{map[string]any{"provider": "edge_adapter", "event_type": "fact.created", "external_id": "event-1", "payload": map[string]any{"sequence": 1}}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"events": []any{}}, ExpectedErrorCodes: []string{"backend.integration.offline_recovery.batch_size_invalid"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_runtime.go", Symbol: "IntegrationOfflineEventRecoveryRequest"}, {Kind: "service", Path: "runtime/application/integration/integration_application_events.go", Symbol: "IntegrationApplicationService.RecoverOfflineIntegrationEvents"}},
	}
}

func integrationInvocationListAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "connector_key", Type: "connector_key"}, {Key: "record_id", Type: "record_id"}, {Key: "workflow_execution_id", Type: "workflow_execution_id"},
		{Key: "status", Type: "string", Enum: integrationmodel.RuntimeIntegrationInvocationStatuses()}, {Key: "provider", Type: "string"}, {Key: "external_principal", Type: "string"},
		{Key: "limit", Type: "integer", Default: 100, Minimum: integrationFloatPointer(1), Maximum: integrationFloatPointer(200)},
	}
	return integrationReadCapability("integration.invocation.list", "GET /integrations/invocations", parameters, "invocations", integrationInvocationSchema(), "IntegrationApplicationService.ListIntegrationInvocations", []capabilitycontract.CapabilityAuthoringReference{{Kind: "connector_key", InputJSONPointer: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connector_key"}})
}

func integrationEventListAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "provider", Type: "string"}, {Key: "status", Type: "string", Enum: integrationmodel.RuntimeIntegrationEventStatuses()},
		{Key: "limit", Type: "integer", Default: 100, Minimum: integrationFloatPointer(1), Maximum: integrationFloatPointer(100)},
	}
	return integrationReadCapability("integration.event.list", "GET /integrations/events", parameters, "events", integrationEventSchema(), "IntegrationApplicationService.ListIntegrationEvents", nil)
}

func integrationOutboxListAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "connector_key", Type: "connector_key"}, {Key: "status", Type: "string", Enum: integrationmodel.RuntimeIntegrationOutboxStatuses()},
		{Key: "limit", Type: "integer", Default: 100, Minimum: integrationFloatPointer(1), Maximum: integrationFloatPointer(200)},
	}
	return integrationReadCapability("integration.outbox.list", "GET /integrations/outbox", parameters, "messages", integrationOutboxMessageSchema(), "IntegrationApplicationService.ListIntegrationOutboxMessages", []capabilitycontract.CapabilityAuthoringReference{{Kind: "connector_key", InputJSONPointer: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connector_key"}})
}

func integrationReadCapability(key, route string, parameters []capabilitycontract.CapabilityAuthoringParameter, collectionKey string, item capabilitycontract.CapabilityAuthoringSchema, symbol string, references []capabilitycontract.CapabilityAuthoringReference) capabilitycontract.CapabilityAuthoringDefinition {
	closed := false
	input := integrationParameterObjectSchema(parameters)
	output := capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{collectionKey, "count"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		collectionKey: {Type: "array", Items: &item}, "count": {Type: "integer", Minimum: integrationFloatPointer(0)},
	}}
	representative := map[string]any{"limit": 25}
	if key == "integration.invocation.list" || key == "integration.outbox.list" {
		representative["connector_key"] = "webhook"
	}
	if key == "integration.event.list" {
		representative["provider"] = "slack"
	}
	invalidLimit := 201
	if key == "integration.event.list" {
		invalidLimit = 101
	}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: key, Status: "supported", Lifecycle: "read_only_activity_query", Parameters: parameters, Permissions: []string{"integration.audit.view"},
		ConfigurationRoutes: []string{route}, FrontendSupportKey: "integration.activity-recovery.v1", InputSchema: input, OutputSchema: &output,
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: collectionKey, JSONPointer: "/" + collectionKey, Type: "integration_activity_list", VisibleTo: "subsequent_capability_calls"}, {Name: "count", JSONPointer: "/count", Type: "integer", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: references,
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{collectionKey}, Transaction: "read_only", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "integration.audit.view"},
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.query_limit_invalid", FieldPath: "limit", ParameterKeys: []string{"minimum", "maximum", "actual"}, MessageKey: "backend.integration.query_limit_invalid"}},
		Examples:           []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{}}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: map[string]any{"limit": invalidLimit}, ExpectedErrorCodes: []string{"backend.integration.query_limit_invalid"}}},
		Sources:            []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/integration/validation/integration_query_validation.go", Symbol: "IntegrationValidateReadLimit"}, {Kind: "service", Path: "runtime/application/integration/integration_application_delivery_management.go", Symbol: symbol}},
	}
}

func integrationOutboxEnqueueAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	open := true
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: "connector_key", Type: "connector_key", Required: true}, {Key: "connection_key", Type: "connection_key"}, {Key: "operation", Type: "operation_key", Required: true}, {Key: "payload", Type: "object"}, {Key: "event_id", Type: "string"}, {Key: "request_ref", Type: "idempotency_key"}, {Key: "dedup_key", Type: "string"}}
	input := integrationParameterObjectSchema(parameters)
	input.Properties["payload"] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.outbox.enqueue", Status: "supported", Lifecycle: "queued_delivery_enqueue", Requires: []string{"integration.connector_operation"}, Parameters: parameters,
		Permissions: []string{"integration.invoke"}, AuditEvents: []string{"integration_outbox_enqueued"}, ConfigurationRoutes: []string{"POST /integrations/outbox"}, FrontendSupportKey: "integration.activity-recovery.v1",
		InputSchema: input, OutputSchema: integrationOutboxMessageSchemaPointer(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "message_id", JSONPointer: "/id", Type: "integration_outbox_id", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "connector_key", InputJSONPointer: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connector_key"}, {Kind: "operation_key", InputJSONPointer: "/operation", ScopeFrom: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/operation_key"}, {Kind: "connection_key", InputJSONPointer: "/connection_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connection_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connector_definition", "integration.connection"}, WriteSet: []string{"integration.outbox"}, Transaction: "integration_outbox_transaction", Idempotency: "request_ref_or_idempotency_key", SideEffects: []string{"integration_outbox_enqueued"}, SideEffectLevel: "internal", Compensation: "deduplicated_queue_entry_can_be_cancelled_before_delivery", PermissionModel: "integration.invoke"},
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.outbox.missing_identity", FieldPath: "connector_key", MessageKey: "backend.integration.outbox.missing_identity"}, {Code: "backend.integration.connector.not_found", FieldPath: "connector_key", MessageKey: "backend.integration.connector.not_found"}, {Code: "backend.idempotency.key_required", FieldPath: "request_ref", MessageKey: "backend.idempotency.key_required"}, {Code: "backend.idempotency.key_mismatch", FieldPath: "request_ref", MessageKey: "backend.idempotency.key_mismatch"}},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"connector_key": "webhook", "operation": "send", "request_ref": "order-1001"}},
			{Name: "representative", Value: map[string]any{"connector_key": "webhook", "connection_key": "webhook_primary", "operation": "send", "payload": map[string]any{"order_id": "1001"}, "request_ref": "order-1001", "dedup_key": "order-1001"}},
			{Name: "invalid_with_repair", Value: map[string]any{"connector_key": "webhook", "request_ref": "order-1001"}, ExpectedErrorCodes: []string{"backend.integration.outbox.missing_identity"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_runtime.go", Symbol: "IntegrationOutboxEnqueueRequest"}, {Kind: "service", Path: "runtime/application/integration/integration_application_delivery_commands.go", Symbol: "IntegrationApplicationService.EnqueueIntegrationOutboxMessage"}},
	}
}

func integrationOutboxStatusAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: "message_id", Type: "integration_outbox_id", Required: true}, {Key: "status", Type: "string", Required: true, Enum: integrationmodel.RuntimeIntegrationOutboxStatuses()}, {Key: "response_ref", Type: "string"}, {Key: "error", Type: "string"}}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.outbox.status", Status: "supported", Lifecycle: "audited_outbox_status_transition", Requires: []string{"integration.outbox.enqueue"}, Parameters: parameters,
		Permissions: []string{"integration.invoke"}, AuditEvents: []string{"integration_outbox_status_updated"}, ConfigurationRoutes: []string{"POST /integrations/outbox/{messageID}/status"}, ResourceKeyPathParameter: "messageID", FrontendSupportKey: "integration.activity-recovery.v1",
		InputSchema: integrationParameterObjectSchema(parameters), OutputSchema: integrationOutboxMessageSchemaPointer(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "message_id", JSONPointer: "/id", Type: "integration_outbox_id", VisibleTo: "subsequent_capability_calls"}, {Name: "status", JSONPointer: "/status", Type: "string", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.outbox"}, WriteSet: []string{"integration.outbox"}, Transaction: "integration_outbox_transaction", Idempotency: "message_id_and_target_status", SideEffects: []string{"integration_outbox_status_updated"}, SideEffectLevel: "internal", PermissionModel: "integration.invoke"},
		Errors:          []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.outbox.invalid_status", FieldPath: "status", MessageKey: "backend.integration.outbox.invalid_status"}, {Code: "backend.integration.outbox.not_found", FieldPath: "message_id", MessageKey: "backend.integration.outbox.not_found"}},
		Examples:        []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"message_id": "outbox-1001", "status": "sent"}}, {Name: "representative", Value: map[string]any{"message_id": "outbox-1001", "status": "failed", "error": "provider_timeout"}}, {Name: "invalid_with_repair", Value: map[string]any{"message_id": "outbox-1001", "status": "unknown"}, ExpectedErrorCodes: []string{"backend.integration.outbox.invalid_status"}}},
		Sources:         []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_runtime.go", Symbol: "IntegrationOutboxStatusRequest"}, {Kind: "service", Path: "runtime/application/integration/integration_application_delivery_management.go", Symbol: "IntegrationApplicationService.UpdateIntegrationOutboxStatus"}},
	}
}

func integrationOutboxRetryAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: "message_id", Type: "integration_outbox_id", Required: true}, {Key: "delay_seconds", Type: "integer", Minimum: integrationFloatPointer(0)}, {Key: "error", Type: "string"}}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.outbox.retry", Status: "supported", Lifecycle: "audited_outbox_retry", Requires: []string{"integration.outbox.enqueue"}, Parameters: parameters,
		Permissions: []string{"integration.retry"}, AuditEvents: []string{"integration_outbox_retry_scheduled"}, ConfigurationRoutes: []string{"POST /integrations/outbox/{messageID}/retry"}, ResourceKeyPathParameter: "messageID", FrontendSupportKey: "integration.activity-recovery.v1",
		InputSchema: integrationParameterObjectSchema(parameters), OutputSchema: integrationOutboxMessageSchemaPointer(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "message_id", JSONPointer: "/id", Type: "integration_outbox_id", VisibleTo: "subsequent_capability_calls"}, {Name: "status", JSONPointer: "/status", Type: "string", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.outbox", "integration.connection", "integration.secret"}, WriteSet: []string{"integration.outbox"}, Transaction: "integration_outbox_transaction", Idempotency: "owner_operation_receipt", SideEffects: []string{"integration_outbox_retry_scheduled"}, SideEffectLevel: "external_deferred", Compensation: "quarantined_or_uncertain_outcomes_require_reconciliation_and_are_never_blind_retried", PermissionModel: "integration.retry"},
		Errors:          []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.outbox.not_found", FieldPath: "message_id", MessageKey: "backend.integration.outbox.not_found"}, {Code: "backend.integration.outbox.reconciliation_required", FieldPath: "message_id", MessageKey: "backend.integration.outbox.reconciliation_required"}, {Code: "backend.integration.outbox.not_retryable", FieldPath: "message_id", MessageKey: "backend.integration.outbox.not_retryable"}, {Code: "backend.integration.outbox.connection_unavailable", FieldPath: "message_id", MessageKey: "backend.integration.outbox.connection_unavailable"}},
		Examples:        []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"message_id": "outbox-1001"}}, {Name: "representative", Value: map[string]any{"message_id": "outbox-1001", "delay_seconds": 60, "error": "provider_timeout"}}, {Name: "invalid_with_repair", Value: map[string]any{"message_id": "quarantined-outbox"}, ExpectedErrorCodes: []string{"backend.integration.outbox.reconciliation_required"}}},
		Sources:         []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_runtime.go", Symbol: "IntegrationOutboxRetryRequest"}, {Kind: "service", Path: "runtime/application/integration/integration_application_delivery_management.go", Symbol: "IntegrationApplicationService.ScheduleIntegrationOutboxRetry"}},
	}
}

func integrationParameterObjectSchema(parameters []capabilitycontract.CapabilityAuthoringParameter) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	properties, required := map[string]capabilitycontract.CapabilityAuthoringSchema{}, []string{}
	for _, parameter := range parameters {
		property := capabilitycontract.CapabilityAuthoringSchema{Type: "string", Default: parameter.Default, Minimum: parameter.Minimum, Maximum: parameter.Maximum}
		if parameter.Type == "integer" {
			property.Type = "integer"
		}
		for _, value := range parameter.Enum {
			property.Enum = append(property.Enum, value)
		}
		properties[parameter.Key] = property
		if parameter.Required {
			required = append(required, parameter.Key)
		}
	}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: properties, Required: required}
}

func integrationInvocationSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"id", "connector_key", "operation", "status"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"id": {Type: "string"}, "workspace_id": {Type: "string"}, "connector_key": {Type: "string"}, "provider_key": {Type: "string"}, "connection_key": {Type: "string"}, "operation": {Type: "string"}, "status": {Type: "string", Enum: integrationAnyEnums(integrationmodel.RuntimeIntegrationInvocationStatuses())}, "duration_ms": {Type: "integer"}, "request_ref": {Type: "string"}, "response_ref": {Type: "string"}, "error": {Type: "string"}, "event_id": {Type: "string"}, "object_key": {Type: "string"}, "record_id": {Type: "string"}, "workflow_execution_id": {Type: "string"}, "metadata": {Type: "object", AdditionalProperties: &open}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
}

func integrationEventSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"id", "provider", "event_type", "external_id", "status", "attempt_count", "fencing_token"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"id": {Type: "string"}, "workspace_id": {Type: "string"}, "provider": {Type: "string"}, "event_type": {Type: "string"}, "external_id": {Type: "string"}, "status": {Type: "string", Enum: integrationAnyEnums(integrationmodel.RuntimeIntegrationEventStatuses())}, "payload": {Type: "object", AdditionalProperties: &open}, "error": {Type: "string"}, "attempt_count": {Type: "integer"}, "next_retry_at": {Type: "string", Format: "date-time"}, "last_attempt_at": {Type: "string", Format: "date-time"}, "lease_owner": {Type: "string"}, "lease_expires_at": {Type: "string", Format: "date-time"}, "fencing_token": {Type: "integer"}, "received_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
}

func integrationOutboxMessageSchemaPointer() *capabilitycontract.CapabilityAuthoringSchema {
	schema := integrationOutboxMessageSchema()
	schema.Schema = "https://json-schema.org/draft/2020-12/schema"
	return &schema
}

func integrationOutboxMessageSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"id", "connector_key", "operation", "status", "attempt_count"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"id": {Type: "string"}, "workspace_id": {Type: "string"}, "connector_key": {Type: "string"}, "connection_key": {Type: "string"}, "operation": {Type: "string"}, "status": {Type: "string", Enum: integrationAnyEnums(integrationmodel.RuntimeIntegrationOutboxStatuses())}, "payload": {Type: "object", AdditionalProperties: &open}, "event_id": {Type: "string"}, "request_ref": {Type: "string"}, "dedup_key": {Type: "string"}, "request_fingerprint": {Type: "string"}, "response_ref": {Type: "string"}, "error": {Type: "string"}, "attempt_count": {Type: "integer"}, "next_attempt_at": {Type: "string", Format: "date-time"}, "last_attempt_at": {Type: "string", Format: "date-time"}, "lease_owner": {Type: "string"}, "lease_expires_at": {Type: "string", Format: "date-time"}, "fencing_token": {Type: "integer"}, "created_by": {Type: "string"}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
}
