package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// Subject evidence fences and receipts contain only stable ownership IDs and
// sanitized plans. They use Runtime's migration lock and sole host ledger.
func EnsureSubjectErasureSchema(ctx context.Context, store Store) error {
	key := ormschema.TextKey(191)
	for _, builder := range []*ormschema.TableBuilder{
		ormschema.NewTable(store.RuntimeRenderer(), "_subject_evidence_erasure_fences").IfNotExists().Columns(
			ormschema.Column("workspace_id", key).NotNull(), ormschema.Column("kind", key).NotNull(),
			ormschema.Column("object_key", key).NotNull(), ormschema.Column("record_id", key).NotNull(),
			ormschema.Column("request_id", key).NotNull(),
		).PrimaryKey("workspace_id", "kind", "object_key", "record_id"),
		ormschema.NewTable(store.RuntimeRenderer(), "_subject_evidence_erasure_receipts").IfNotExists().Columns(
			ormschema.Column("workspace_id", key).NotNull(), ormschema.Column("request_id", key).NotNull(),
			ormschema.Column("subject_id", key).NotNull(), ormschema.Column("plan_json", ormschema.LongText()).NotNull(),
			ormschema.Column("result_json", ormschema.LongText()).NotNull(),
		).PrimaryKey("workspace_id", "request_id"),
	} {
		statement, args, err := builder.Build()
		if err != nil {
			return err
		}
		if _, err = store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("create Runtime subject erasure schema: %w", err)
		}
	}
	return nil
}
