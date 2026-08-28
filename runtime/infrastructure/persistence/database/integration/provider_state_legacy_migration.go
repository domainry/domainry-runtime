package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// MigrateLegacyConnectorProviderStates is an upgrade bridge only. New schema
// creation never creates the Gmail-specific tables.
func (r IntegrationConfigStore) MigrateLegacyConnectorProviderStates(ctx context.Context, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return err
	}
	for _, legacy := range []struct {
		table, task, columns string
	}{
		{"integration_gmail_sync_states", "gmail_sync", "account_email,history_id,status,next_poll_at,last_error_code,attempt_count,fencing_token,updated_at"},
		{"integration_gmail_watch_states", "gmail_watch", "project_id,topic_id,subscription_id,status,expires_at,next_renew_at,next_pull_at,last_error_code,attempt_count,fencing_token,updated_at"},
	} {
		exists, err := r.legacyProviderStateTableExists(ctx, legacy.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := r.migrateLegacyProviderStateTable(ctx, legacy.table, legacy.task, legacy.columns); err != nil {
			return err
		}
	}
	return nil
}

func (r IntegrationConfigStore) legacyProviderStateTableExists(ctx context.Context, table string) (bool, error) {
	var count int
	var err error
	switch r.driver {
	case "postgres":
		schema := strings.TrimSpace(r.store.DatabaseSchema())
		if schema == "" {
			schema = "public"
		}
		err = r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema="+r.store.Placeholder(1)+" AND table_name="+r.store.Placeholder(2), schema, table).Scan(&count)
	case "mysql":
		err = r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name="+r.store.Placeholder(1), table).Scan(&count)
	default:
		err = r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name="+r.store.Placeholder(1), table).Scan(&count)
	}
	return count > 0, err
}

func (r IntegrationConfigStore) migrateLegacyProviderStateTable(ctx context.Context, table, task, columns string) error {
	rows, err := r.db.QueryContext(ctx, "SELECT workspace_id,connection_key,"+columns+" FROM "+r.store.TableIdentifier(table))
	if err != nil {
		return fmt.Errorf("read legacy %s: %w", table, err)
	}
	type record struct{ values []any }
	records := []record{}
	for rows.Next() {
		width := 10
		if task == "gmail_watch" {
			width = 13
		}
		values := make([]any, width)
		pointers := make([]any, width)
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			_ = rows.Close()
			return err
		}
		records = append(records, record{values: values})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, record := range records {
		workspace, connection := fmt.Sprint(record.values[0]), fmt.Sprint(record.values[1])
		var count int
		if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND connection_key="+r.store.Placeholder(2)+" AND task_key="+r.store.Placeholder(3), workspace, connection, task).Scan(&count); err != nil || count != 0 {
			if err != nil {
				return err
			}
			continue
		}
		payload := map[string]any{}
		due, lastError, attempt, fencing, updated := "", "", 0, int64(0), ""
		if task == "gmail_sync" {
			payload["account_email"], payload["history_id"] = fmt.Sprint(record.values[2]), fmt.Sprint(record.values[3])
			due, lastError = fmt.Sprint(record.values[5]), fmt.Sprint(record.values[6])
			attempt, fencing, updated = legacyInt(record.values[7]), legacyInt64(record.values[8]), fmt.Sprint(record.values[9])
		} else {
			payload["project_id"], payload["topic_id"], payload["subscription_id"] = fmt.Sprint(record.values[2]), fmt.Sprint(record.values[3]), fmt.Sprint(record.values[4])
			payload["expires_at"], payload["next_renew_at"] = fmt.Sprint(record.values[6]), fmt.Sprint(record.values[7])
			due, lastError = fmt.Sprint(record.values[8]), fmt.Sprint(record.values[9])
			attempt, fencing, updated = legacyInt(record.values[10]), legacyInt64(record.values[11]), fmt.Sprint(record.values[12])
		}
		raw, _ := json.Marshal(payload)
		values := []any{"connector_provider_state:" + workspace + ":google_workspace:google:" + connection + ":" + task, workspace, "google_workspace", "google", connection, task, 1, string(raw), "retry", due, lastError, attempt, "", "", fencing, updated}
		columns := []string{"id", "workspace_id", "connector_key", "provider_key", "connection_key", "task_key", "state_version", "payload_json", "status", "due_at", "last_error_code", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token", "updated_at"}
		if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("connector_provider_states")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(values))+")", values...); err != nil {
			return fmt.Errorf("migrate legacy %s: %w", table, err)
		}
	}
	return nil
}

func legacyInt(value any) int {
	var result int
	_, _ = fmt.Sscan(fmt.Sprint(value), &result)
	return result
}
func legacyInt64(value any) int64 {
	var result int64
	_, _ = fmt.Sscan(fmt.Sprint(value), &result)
	return result
}
