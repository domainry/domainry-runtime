package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

const ReportExportPrepareReceiptTable = "_report_export_prepare_receipts"

// EnsureReportExportPrepareReceiptSchema creates the Runtime-owned durable
// coordination receipt through domainry-orm. It runs inside the host migration
// lock and is recorded only in the host _schema_migrations ledger.
func EnsureReportExportPrepareReceiptSchema(ctx context.Context, store Store) error {
	key := ormschema.TextKey(255)
	statement, arguments, err := ormschema.NewTable(store.RuntimeRenderer(), ReportExportPrepareReceiptTable).
		IfNotExists().
		Columns(
			ormschema.Column("id", key).NotNull(),
			ormschema.Column("operation_id", key).NotNull(),
			ormschema.Column("workspace_id", key).NotNull(),
			ormschema.Column("requester_user_id", key).NotNull(),
			ormschema.Column("use_case", key).NotNull(),
			ormschema.Column("report_key", key).NotNull(),
			ormschema.Column("object_key", key).NotNull(),
			ormschema.Column("audit_id", key).NotNull(),
			ormschema.Column("idempotency_key", ormschema.TextKey(71)).NotNull(),
			ormschema.Column("request_fingerprint", key).NotNull(),
			ormschema.Column("status", key).NotNull(),
			ormschema.Column("payload_json", ormschema.Text()).NotNull().DefaultValue(""),
			ormschema.Column("business_job_key", key).NotNull().DefaultValue(""),
			ormschema.Column("job_id", key).NotNull().DefaultValue(""),
			ormschema.Column("completion_artifact_id", key).NotNull().DefaultValue(""),
			ormschema.Column("completion_fingerprint", key).NotNull().DefaultValue(""),
			ormschema.Column("terminal_error_code", key).NotNull().DefaultValue(""),
			ormschema.Column("lease_owner", key).NotNull(),
			ormschema.Column("lease_expires_at", key).NotNull(),
			ormschema.Column("fencing_token", ormschema.BigInt()).NotNull().DefaultValue(1),
			ormschema.Column("created_at", key).NotNull(),
			ormschema.Column("updated_at", key).NotNull(),
			ormschema.Column("expires_at", key).NotNull().DefaultValue(""),
		).
		PrimaryKey("workspace_id", "id").
		Build()
	if err != nil {
		return fmt.Errorf("build report export prepare receipt table: %w", err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("create report export prepare receipt table: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, ReportExportPrepareReceiptTable, "uniq_report_export_prepare_operation", true, "workspace_id", "operation_id"); err != nil {
		return fmt.Errorf("create report export prepare operation identity: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, ReportExportPrepareReceiptTable, "uniq_report_export_prepare_caller", true, "workspace_id", "operation_id", "idempotency_key"); err != nil {
		return fmt.Errorf("create report export prepare caller identity: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, ReportExportPrepareReceiptTable, "idx_report_export_prepare_lease", false, "workspace_id", "status", "lease_expires_at"); err != nil {
		return fmt.Errorf("create report export prepare lease index: %w", err)
	}
	return nil
}
