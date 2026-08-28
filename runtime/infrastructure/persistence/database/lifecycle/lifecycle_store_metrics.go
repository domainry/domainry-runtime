package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (s LifecycleStore) Metrics(ctx context.Context, workspaceID string, now time.Time) (lifecyclemodel.Metrics, error) {
	metrics := lifecyclemodel.Metrics{}
	query := "SELECT COUNT(*), MIN(" + s.store.Identifier("updated_at") + ") FROM " + s.store.TableIdentifier("lifecycle_cleanup_jobs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("status") + " IN (" + s.store.Placeholder(2) + ", " + s.store.Placeholder(3) + ", " + s.store.Placeholder(4) + ")"
	var oldest sql.NullString
	if err := s.db.QueryRowContext(ctx, query, workspaceID, lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible, _ = time.Parse(time.RFC3339Nano, oldest.String)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.store.TableIdentifier("lifecycle_legal_holds")+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1)+" AND "+s.store.Identifier("starts_at")+" <= "+s.store.Placeholder(2)+" AND ("+s.store.Identifier("ends_at")+" = '' OR "+s.store.Identifier("ends_at")+" > "+s.store.Placeholder(3)+")", workspaceID, lifecycleTime(now), lifecycleTime(now)).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.store.Identifier("event")+", "+s.store.Identifier("payload_json")+" FROM "+s.store.TableIdentifier("lifecycle_audit_evidence")+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1)+" AND "+s.store.Identifier("event")+" IN ("+s.store.Placeholder(2)+", "+s.store.Placeholder(3)+")", workspaceID, "lifecycle.cleanup.succeeded", "lifecycle.cleanup.failed")
	if err != nil {
		return metrics, err
	}
	defer rows.Close()
	for rows.Next() {
		var event, payload string
		if err := rows.Scan(&event, &payload); err != nil {
			return metrics, err
		}
		if event == "lifecycle.cleanup.failed" {
			metrics.FailureTotal++
			continue
		}
		var evidence lifecyclemodel.AuditEvidence
		var job lifecyclemodel.CleanupJob
		if json.Unmarshal([]byte(payload), &evidence) == nil && json.Unmarshal(evidence.Payload, &job) == nil {
			metrics.PurgedTotal += job.Purged
		}
	}
	if err := rows.Err(); err != nil {
		return metrics, err
	}
	metrics.Warning = metrics.FailureTotal > 0 || metrics.EligibleBacklog > 1000 || (!metrics.OldestEligible.IsZero() && now.Sub(metrics.OldestEligible) > 24*time.Hour)
	return metrics, nil
}

func lifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseLifecycleTime(value string) time.Time {
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}

func lifecycleColumns(store *database.RuntimeStore, columns ...string) string {
	result := ""
	for index, column := range columns {
		if index > 0 {
			result += ", "
		}
		result += store.Identifier(column)
	}
	return result
}
