package schema

import (
	"context"
	"fmt"
)

func EnsureMetadataSchema(ctx context.Context, s Store) error {
	notificationIndexText := s.MetadataIDColumnType()
	documentText := "TEXT"
	if s.Driver() == "mysql" {
		// Notification routing fields are machine identities, keys, states, and
		// RFC3339 timestamps. Keep their full identifier budget while using an
		// ASCII collation so the widest inbox composite index stays below
		// InnoDB's 3072-byte key limit.
		notificationIndexText = "VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin"
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
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_template_records")+" ("+
		s.Identifier("template_key")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("draft_json")+" "+documentText+", "+
		s.Identifier("published_json")+" "+documentText+", "+
		s.Identifier("published_version")+" INTEGER NOT NULL DEFAULT 0, "+
		s.Identifier("status")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT 'active', "+
		s.Identifier("updated_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_template_records: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_template_versions")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("template_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("version")+" INTEGER NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("content_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("published_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("published_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_template_versions: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_template_versions", "uniq_notification_template_version", true, "template_key", "version"); err != nil {
		return fmt.Errorf("create notification template version index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_template_publication_requests")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("template_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("snapshot_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("candidate_hash")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("draft_updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("status")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("scheduled_for")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("requested_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("requested_at")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("reviewed_by")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("reviewed_at")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("published_version")+" INTEGER NOT NULL DEFAULT 0, "+
		// Every insert supplies failure explicitly. Avoid a default on TEXT so
		// this table remains valid on MySQL configurations that reject TEXT
		// defaults while still allowing full provider/runtime error evidence.
		s.Identifier("failure")+" TEXT NOT NULL, "+
		s.Identifier("lease_owner")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("lease_expires_at")+" "+s.MetadataIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("fencing_token")+" BIGINT NOT NULL DEFAULT 0, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_template_publication_requests: %w", err)
	}
	for column, definition := range map[string]string{
		"lease_owner":      s.MetadataIDColumnType() + " NOT NULL DEFAULT ''",
		"lease_expires_at": s.MetadataIDColumnType() + " NOT NULL DEFAULT ''",
		"fencing_token":    "BIGINT NOT NULL DEFAULT 0",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "notification_template_publication_requests", column, definition); err != nil {
			return fmt.Errorf("ensure notification publication %s: %w", column, err)
		}
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_template_publication_requests", "idx_notification_publication_template", false, "template_key", "requested_at"); err != nil {
		return fmt.Errorf("create notification publication template index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_template_publication_requests", "idx_notification_publication_due", false, "status", "scheduled_for"); err != nil {
		return fmt.Errorf("create notification publication due index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_template_publication_locks")+" ("+
		s.Identifier("template_key")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("request_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_template_publication_locks: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_delivery_policy")+" ("+
		s.Identifier("policy_key")+" "+s.MetadataIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("updated_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_delivery_policy: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_recipient_preferences")+" ("+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("recipient_key")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("updated_by")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.MetadataIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_recipient_preferences: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_recipient_preferences", "uniq_notification_recipient_preference", true, "workspace_id", "recipient_key"); err != nil {
		return fmt.Errorf("create notification recipient preference index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_delivery_reservations")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("recipient_key")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("template_key")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("channel")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("dedupe_key")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_delivery_reservations: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_delivery_reservations", "idx_notification_delivery_frequency", false, "workspace_id", "recipient_key", "channel", "created_at"); err != nil {
		return fmt.Errorf("create notification delivery frequency index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_delivery_reservations", "uniq_notification_delivery_reservation_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification delivery reservation workspace identity: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_delivery_reservations", "idx_notification_delivery_dedupe", false, "workspace_id", "recipient_key", "template_key", "channel", "dedupe_key"); err != nil {
		return fmt.Errorf("create notification delivery dedupe index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_events")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("source")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("source_event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("status")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("attempt_count")+" INTEGER NOT NULL DEFAULT 0, "+
		s.Identifier("next_attempt_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("last_error_code")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("lease_owner")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("lease_expires_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("fencing_token")+" BIGINT NOT NULL DEFAULT 0, "+
		s.Identifier("occurred_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_events: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_events", "uniq_notification_event_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification event identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_events", "uniq_notification_event_source_identity", true, "workspace_id", "source", "source_event_id"); err != nil {
		return fmt.Errorf("create notification event source index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_events", "idx_notification_event_due", false, "status", "next_attempt_at", "lease_expires_at"); err != nil {
		return fmt.Errorf("create notification event due index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_event_failures")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("event_type")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("source")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("source_event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("stage")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("error_code")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("attempt")+" INTEGER NOT NULL, "+
		s.Identifier("disposition")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("retryable")+" INTEGER NOT NULL DEFAULT 0, "+
		s.Identifier("next_attempt_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("fencing_token")+" BIGINT NOT NULL, "+
		s.Identifier("occurred_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_event_failures: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_event_failures", "uniq_notification_event_failure_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification event failure identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_event_failures", "uniq_notification_event_failure_attempt", true, "workspace_id", "event_id", "fencing_token"); err != nil {
		return fmt.Errorf("create notification event failure attempt index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_event_failures", "idx_notification_event_failure_governance", false, "workspace_id", "occurred_at", "stage", "error_code"); err != nil {
		return fmt.Errorf("create notification event failure governance index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_channel_plans")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("channel")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("status")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("attempt_count")+" INTEGER NOT NULL DEFAULT 0, "+
		s.Identifier("next_attempt_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("last_error_code")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("outbox_message_id")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("lease_owner")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("lease_expires_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("fencing_token")+" BIGINT NOT NULL DEFAULT 0, "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_channel_plans: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_channel_plans", "uniq_notification_channel_plan_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification channel plan identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_channel_plans", "idx_notification_channel_plan_due", false, "status", "next_attempt_at", "lease_expires_at", "created_at"); err != nil {
		return fmt.Errorf("create notification channel plan due index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_inbox_items")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("recipient_user_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("surface")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("event_type")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("source")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("category")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("severity")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("title")+" "+documentText+" NOT NULL, "+
		s.Identifier("body")+" "+documentText+" NOT NULL, "+
		s.Identifier("search_text")+" "+documentText+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("subject_type")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("subject_id")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("action_state")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("alert_state")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("group_key")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("occurrence_count")+" INTEGER NOT NULL DEFAULT 1, "+
		s.Identifier("first_occurred_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("last_occurred_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("read_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("archived_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("expires_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_inbox_items: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_items", "uniq_notification_inbox_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification inbox identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_items", "idx_notification_inbox_mailbox", false, "workspace_id", "recipient_user_id", "surface", "archived_at", "updated_at", "id"); err != nil {
		return fmt.Errorf("create notification inbox mailbox index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_items", "idx_notification_inbox_unread", false, "workspace_id", "recipient_user_id", "surface", "read_at", "archived_at"); err != nil {
		return fmt.Errorf("create notification inbox unread index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_items", "idx_notification_inbox_facets", false, "workspace_id", "recipient_user_id", "surface", "category", "source", "severity", "archived_at"); err != nil {
		return fmt.Errorf("create notification inbox facets index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_items", "idx_notification_inbox_group", false, "workspace_id", "recipient_user_id", "surface", "group_key", "alert_state"); err != nil {
		return fmt.Errorf("create notification inbox group index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_inbox_delegations")+" ("+
		s.Identifier("id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("owner_user_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("delegate_user_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("surface")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("starts_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("ends_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("enabled")+" BOOLEAN NOT NULL DEFAULT TRUE, "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_inbox_delegations: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_delegations", "uniq_notification_inbox_delegation_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create notification inbox delegation identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_delegations", "idx_notification_inbox_delegation_delegate", false, "workspace_id", "delegate_user_id", "surface", "enabled", "starts_at", "ends_at"); err != nil {
		return fmt.Errorf("create notification inbox delegation delegate index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_alert_groups")+" ("+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("recipient_user_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("surface")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("group_key")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("state")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("occurrence_count")+" INTEGER NOT NULL DEFAULT 1, "+
		s.Identifier("first_occurred_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("last_occurred_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("acknowledged_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("acknowledged_by")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("resolved_at")+" "+notificationIndexText+" NOT NULL DEFAULT '', "+
		s.Identifier("last_event_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_alert_groups: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_alert_groups", "uniq_notification_alert_group", true, "workspace_id", "recipient_user_id", "surface", "group_key"); err != nil {
		return fmt.Errorf("create notification alert group identity index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_alert_groups", "idx_notification_alert_group_state", false, "workspace_id", "state", "updated_at"); err != nil {
		return fmt.Errorf("create notification alert group state index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("notification_inbox_saved_views")+" ("+
		s.Identifier("workspace_id")+" "+s.MetadataIDColumnType()+" NOT NULL, "+
		s.Identifier("recipient_user_id")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("surface")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("view_key")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
		s.Identifier("created_at")+" "+notificationIndexText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+notificationIndexText+" NOT NULL)"); err != nil {
		return fmt.Errorf("create notification_inbox_saved_views: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "notification_inbox_saved_views", "uniq_notification_inbox_saved_view", true, "workspace_id", "recipient_user_id", "surface", "view_key"); err != nil {
		return fmt.Errorf("create notification inbox saved view index: %w", err)
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
