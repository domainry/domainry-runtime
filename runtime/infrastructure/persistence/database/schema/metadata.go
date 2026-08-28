package schema

import (
	"context"
	"fmt"
)

func EnsureMetadataSchema(ctx context.Context, s Store) error {
	documentText := "TEXT"
	if s.Driver() == "mysql" {
		// Change Plans and canonical resource definitions are complete system
		// documents, not short labels. MySQL TEXT is capped at 64 KiB while
		// SQLite/PostgreSQL TEXT is effectively unbounded for this use case.
		documentText = "LONGTEXT"
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("metadata_catalog")+" ("+
		s.Identifier("key")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("value")+" "+documentText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create metadata_catalog: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("metadata_exact_decimal_migrations")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("contract_version")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("column_keys")+" "+documentText+" NOT NULL, "+
		s.Identifier("from_types")+" "+documentText+" NOT NULL, "+
		s.Identifier("to_types")+" "+documentText+" NOT NULL, "+
		s.Identifier("row_count")+" BIGINT NOT NULL, "+
		s.Identifier("before_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("after_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("applied_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create metadata_exact_decimal_migrations: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("metadata_definition_versions")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("resource_type")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("resource_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("schema_version")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("schema_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create metadata_definition_versions: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_change_plan_drafts")+" ("+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("plan_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("revision")+" INTEGER NOT NULL, "+
		s.Identifier("status")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("created_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		"PRIMARY KEY ("+s.Identifier("workspace_id")+", "+s.Identifier("plan_id")+"))"); err != nil {
		return fmt.Errorf("create business_change_plan_drafts: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_change_plan_operations")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("plan_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("plan_revision")+" INTEGER NOT NULL, "+
		s.Identifier("operation")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("idempotency_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("request_fingerprint")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("status")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("result_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("lease_owner")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("lease_expires_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("fencing_token")+" BIGINT NOT NULL, "+
		s.Identifier("error_code")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("expires_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("actor_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create business_change_plan_operations: %w", err)
	}
	if err := prepareIdempotencyReceiptMigrations(ctx, s, idempotencyReceiptMigrationSpec{table: "business_change_plan_operations", scopeColumns: []string{"plan_id", "plan_revision", "operation"}, backfillColumns: []string{"plan_id", "operation"}}); err != nil {
		return err
	}
	if err := s.CreateIndexIfMissing(ctx, "business_change_plan_operations", "uniq_change_plan_operation_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create change plan operation workspace identity: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_change_plan_operations", "uniq_change_plan_operation_scope", true, "workspace_id", "plan_id", "plan_revision", "operation", "idempotency_key"); err != nil {
		return fmt.Errorf("create change plan operation unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_change_plan_operations", "idx_change_plan_operation_lease", false, "status", "lease_expires_at"); err != nil {
		return fmt.Errorf("create change plan operation lease index: %w", err)
	}
	for _, table := range metadataDefinitionTables() {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+
			s.Identifier("id")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
			s.Identifier("resource_key")+" "+s.MetadataIDColumnType()+" NOT NULL UNIQUE, "+
			s.Identifier("object_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("name")+" TEXT NOT NULL, "+
			s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
			s.Identifier("schema_version")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("schema_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("source_kind")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("source_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("disabled_at")+" "+s.MetadataIDColumnType()+", "+
			s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
			s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_localized_text")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("entity_type")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("entity_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("property")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text")+" TEXT NOT NULL, "+
		s.Identifier("source_kind")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("source_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create business_localized_text: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "uniq_business_localized_text_key", true,
		"workspace_id", "entity_type", "entity_key", "property", "locale"); err != nil {
		return fmt.Errorf("create business_localized_text unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "uniq_business_localized_text_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create business_localized_text workspace identity: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "idx_business_localized_text_entity", false,
		"entity_type", "entity_key"); err != nil {
		return fmt.Errorf("create business_localized_text entity index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_record_localized_value")+" ("+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("record_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("field_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text_value")+" TEXT NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create business_record_localized_value: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_record_localized_value", "uniq_business_record_localized_value", true,
		"workspace_id", "object_key", "record_id", "field_key", "locale"); err != nil {
		return fmt.Errorf("create business record localized value unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_record_localized_value", "idx_business_record_localized_search", false,
		"workspace_id", "object_key", "locale", "field_key", "record_id"); err != nil {
		return fmt.Errorf("create business record localized search index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("web_push_subscriptions")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("user_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("endpoint_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("endpoint")+" TEXT NOT NULL, "+
		s.Identifier("p256dh")+" TEXT NOT NULL, "+
		s.Identifier("auth_secret")+" TEXT NOT NULL, "+
		s.Identifier("status")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("expires_at")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("revoked_at")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '')"); err != nil {
		return fmt.Errorf("create web_push_subscriptions: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "web_push_subscriptions", "uniq_web_push_subscription_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create web push subscription identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "web_push_subscriptions", "uniq_web_push_subscription_endpoint", true, "workspace_id", "endpoint_hash"); err != nil {
		return fmt.Errorf("create web push subscription endpoint index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "web_push_subscriptions", "idx_web_push_subscription_user", false, "workspace_id", "user_id", "status"); err != nil {
		return fmt.Errorf("create web push subscription user index: %w", err)
	}
	return nil
}

func metadataDefinitionTables() []string {
	return []string{
		"object_definitions",
		"field_definitions",
		"validation_definitions",
		"view_definitions",
		"action_definitions",
		"workflow_definitions",
		"scheduler_definitions",
		"automation_rule_definitions",
		"preference_definitions",
		"rule_set_definitions",
		"report_definitions",
		"operation_state_example_definitions",
		"sensitive_field_policy_definitions",
		"report_export_control_definitions",
		"dictionary_definitions",
		"connector_definitions",
		"integration_event_mapping_definitions",
		"surface_definitions",
		"component_definitions",
		"entrypoint_definitions",
		"skill_definitions",
		"agent_definitions",
		"agent_task_definitions",
		"agent_entrypoint_definitions",
		"agent_service_principal_definitions",
		"identity_profile_binding_definitions",
	}
}
