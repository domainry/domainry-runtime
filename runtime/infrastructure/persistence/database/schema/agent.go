package schema

import (
	"context"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func EnsureAgentSchema(ctx context.Context, store Store) error {
	if err := EnsureAgentStateSchema(ctx, store); err != nil {
		return fmt.Errorf("migrate agent state schema: %w", err)
	}
	if err := EnsureAgentTaskRunSchema(ctx, store); err != nil {
		return fmt.Errorf("migrate agent task run schema: %w", err)
	}
	if err := EnsureAgentInteractiveRunSchema(ctx, store); err != nil {
		return fmt.Errorf("migrate agent interactive run schema: %w", err)
	}
	return nil
}

func EnsureAgentStateSchema(ctx context.Context, store Store) error {
	statement, args, err := ormbuilder.NewCreateTableBuilder(store.RuntimeRenderer(), "agent_runtime_state").WithoutSystemColumns().IfNotExists().Columns(
		ormbuilder.DefineColumn("kind", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("state_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("workspace_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("user_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("role_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("payload_json", ormbuilder.TextType()).NotNull(),
		ormbuilder.DefineColumn("updated_at", ormbuilder.BigIntType()).NotNull(),
	).PrimaryKey("workspace_id", "kind", "state_key").Build()
	if err != nil {
		return err
	}
	_, err = store.SchemaDB().ExecContext(ctx, statement, args...)
	return err
}

func EnsureAgentTaskRunSchema(ctx context.Context, store Store) error {
	statement, args, err := ormbuilder.NewCreateTableBuilder(store.RuntimeRenderer(), "agent_task_runs").WithoutSystemColumns().IfNotExists().Columns(
		ormbuilder.DefineColumn("workspace_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("run_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("idempotency_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("task_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("process_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("status", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("lease_owner", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("fencing_token", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("lease_expires_at", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("next_attempt_at", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("payload_json", ormbuilder.TextType()).NotNull(),
		ormbuilder.DefineColumn("created_at", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("updated_at", ormbuilder.BigIntType()).NotNull(),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key").Build()
	if err != nil {
		return err
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	indexes := []struct {
		name    string
		columns []string
	}{
		{name: "idx_agent_task_claim", columns: []string{"workspace_id", "status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "idx_agent_task_process", columns: []string{"workspace_id", "process_id", "status"}},
		{name: "idx_agent_task_key", columns: []string{"workspace_id", "task_key", "status"}},
	}
	for _, index := range indexes {
		builder := store.RuntimeProfile().ApplyCreateIndex(ormbuilder.NewCreateIndexBuilder(store.RuntimeRenderer(), index.name, "agent_task_runs").Columns(index.columns...))
		statement, args, err := builder.Build()
		if err != nil {
			return err
		}
		if _, err := store.SchemaDB().ExecContext(ctx, statement, args...); err != nil && !store.RuntimeProfile().IsCreateIndexAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func EnsureAgentInteractiveRunSchema(ctx context.Context, store Store) error {
	statement, args, err := ormbuilder.NewCreateTableBuilder(store.RuntimeRenderer(), "agent_interactive_runs").WithoutSystemColumns().IfNotExists().Columns(
		ormbuilder.DefineColumn("workspace_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("run_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("session_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("user_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("role_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("surface", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("status", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("idempotency_key", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("process_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("task_run_id", ormbuilder.TextKeyType(255)).NotNull(),
		ormbuilder.DefineColumn("payload_json", ormbuilder.TextType()).NotNull(),
		ormbuilder.DefineColumn("created_at", ormbuilder.BigIntType()).NotNull(),
		ormbuilder.DefineColumn("updated_at", ormbuilder.BigIntType()).NotNull(),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key").Build()
	if err != nil {
		return err
	}
	_, err = store.SchemaDB().ExecContext(ctx, statement, args...)
	return err
}
