package schema

import (
	"context"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func EnsureUploadSubjectSchema(ctx context.Context, store Store) error {
	key := ormschema.TextKey(191)
	builder := ormschema.NewTable(store.RuntimeRenderer(), "_upload_subject_bindings").IfNotExists().Columns(
		ormschema.Column("workspace_id", key).NotNull(), ormschema.Column("id", key).NotNull(),
		ormschema.Column("filename", key).NotNull(), ormschema.Column("object_key", key).NotNull(),
		ormschema.Column("field_key", key).NotNull(), ormschema.Column("user_id", key).NotNull(),
		ormschema.Column("sha256", key).NotNull(),
	).PrimaryKey("workspace_id", "id")
	statement, args, err := builder.Build()
	if err != nil {
		return err
	}
	if _, err = store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	return store.CreateIndexIfMissing(ctx, "_upload_subject_bindings", "uniq_upload_subject_filename", true, "workspace_id", "filename")
}
