package schema

import (
	"context"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func EnsureRateLimitSchema(ctx context.Context, store Store) error {
	statement, arguments, err := ormbuilder.NewCreateTableBuilder(store.RuntimeRenderer(), "runtime_rate_limit_bucket").
		WithoutSystemColumns().IfNotExists().Columns(
		ormbuilder.DefineColumn("bucket_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("window_start_ns", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("request_count", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("updated_at_ns", ormbuilder.BigIntType()).NotNull(),
	).PrimaryKey("bucket_key").Build()
	if err != nil {
		return fmt.Errorf("build runtime rate-limit schema: %w", err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("create runtime rate-limit schema: %w", err)
	}
	return nil
}
