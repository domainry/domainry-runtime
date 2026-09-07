package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

const DispatchCallbackReceiptTable = "_dispatch_callback_receipts"

// EnsureDispatchCallbackReceiptSchema creates Runtime's protocol receipt using
// domainry-orm under the host migration lock and host _schema_migrations ledger.
func EnsureDispatchCallbackReceiptSchema(ctx context.Context, store Store) error {
	key := ormschema.TextKey(255)
	statement, arguments, err := ormschema.NewTable(store.RuntimeRenderer(), DispatchCallbackReceiptTable).
		IfNotExists().
		Columns(
			ormschema.Column("id", key).NotNull(),
			ormschema.Column("workspace_id", key).NotNull(),
			ormschema.Column("runtime_id", key).NotNull(),
			ormschema.Column("method", ormschema.TextKey(16)).NotNull(),
			ormschema.Column("path", key).NotNull(),
			ormschema.Column("idempotency_key", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("request_fingerprint", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("status", key).NotNull(),
			ormschema.Column("execution_id", key).NotNull(),
			ormschema.Column("downstream_id", key).NotNull().DefaultValue(""),
			ormschema.Column("downstream_owner", key).NotNull().DefaultValue(""),
			ormschema.Column("downstream_status", key).NotNull().DefaultValue(""),
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
		return fmt.Errorf("build dispatch callback receipt table: %w", err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("create dispatch callback receipt table: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, DispatchCallbackReceiptTable, "uniq_dispatch_callback_scope", true, "workspace_id", "runtime_id", "method", "path", "idempotency_key"); err != nil {
		return fmt.Errorf("create dispatch callback scope identity: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, DispatchCallbackReceiptTable, "idx_dispatch_callback_lease", false, "workspace_id", "status", "lease_expires_at"); err != nil {
		return fmt.Errorf("create dispatch callback lease index: %w", err)
	}
	return nil
}
