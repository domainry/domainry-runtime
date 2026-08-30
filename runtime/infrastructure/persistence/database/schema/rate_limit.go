package schema

import (
	"context"
	"fmt"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func EnsureRateLimitSchema(ctx context.Context, store Store) error {
	statement, arguments, err := ormschema.NewTable(store.RuntimeRenderer(), "runtime_rate_limit_bucket").
		IfNotExists().Columns(
		ormschema.Column("bucket_key", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("window_start_ns", ormschema.BigInt()).NotNull(),
		ormschema.Column("request_count", ormschema.BigInt()).NotNull(),
		ormschema.Column("updated_at_ns", ormschema.BigInt()).NotNull(),
	).PrimaryKey("bucket_key").Build()
	if err != nil {
		return fmt.Errorf("build runtime rate-limit schema: %w", err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("create runtime rate-limit schema: %w", err)
	}
	return nil
}
