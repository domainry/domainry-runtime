package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ reportcontract.ReportSnapshotStore = (*ReportSnapshotStore)(nil)

type ReportSnapshotStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewReportSnapshotStore(store *database.RuntimeStore) *ReportSnapshotStore {
	return &ReportSnapshotStore{store: store, db: store.DB()}
}

func (s *ReportSnapshotStore) BeginReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	if err := reportSnapshotRequestValid(request); err != nil {
		return reportmodel.ReportSnapshot{}, false, err
	}
	if current, ok, err := s.reportSnapshotByIdempotency(ctx, request); err != nil {
		return reportmodel.ReportSnapshot{}, false, err
	} else if ok {
		if current.Status == "succeeded" {
			return current, false, nil
		}
		query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "report_snapshots", request.WorkspaceID).Set("status", "refreshing").Set("started_at", request.StartedAt).Set("error_code", "").Where(ormbuilder.Equal("id", current.ID)).Build()
		if buildErr != nil {
			return reportmodel.ReportSnapshot{}, false, buildErr
		}
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			return reportmodel.ReportSnapshot{}, false, err
		}
		current.Status, current.StartedAt, current.ErrorCode = "refreshing", request.StartedAt, ""
		return current, true, nil
	}
	id := reportSnapshotID(request)
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "report_snapshots", request.WorkspaceID).Columns("id", "report_key", "access_scope_hash", "idempotency_key", "status", "summary_json", "watermark", "source_versions_json", "row_count", "source_row_count", "started_at", "refreshed_at", "error_code").Values(id, request.ReportKey, request.AccessScopeHash, request.IdempotencyKey, "refreshing", "{}", "", "{}", 0, 0, request.StartedAt, "", "").Build()
	if buildErr != nil {
		return reportmodel.ReportSnapshot{}, false, buildErr
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		if current, ok, readErr := s.reportSnapshotByIdempotency(ctx, request); readErr == nil && ok {
			return current, current.Status != "succeeded", nil
		}
		return reportmodel.ReportSnapshot{}, false, err
	}
	return reportmodel.ReportSnapshot{ID: id, WorkspaceID: request.WorkspaceID, ReportKey: request.ReportKey, AccessScopeHash: request.AccessScopeHash, IdempotencyKey: request.IdempotencyKey, Status: "refreshing", StartedAt: request.StartedAt}, true, nil
}

func (s *ReportSnapshotStore) CompleteReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotCompleteRequest) error {
	if strings.TrimSpace(request.Snapshot.WorkspaceID) == "" {
		return fmt.Errorf("report snapshot workspace is required")
	}
	// ReportSummary and SourceVersions contain only JSON-safe concrete fields.
	summaryJSON, _ := json.Marshal(request.Snapshot.Summary)
	versionsJSON, _ := json.Marshal(request.Snapshot.SourceVersions)
	builder := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "report_snapshots", request.Snapshot.WorkspaceID)
	query, args, buildErr := builder.Set("status", "succeeded").Set("summary_json", string(summaryJSON)).Set("watermark", request.Snapshot.Watermark).Set("source_versions_json", string(versionsJSON)).Set("row_count", request.Snapshot.Summary.RowCount).Set("source_row_count", request.Snapshot.Summary.SourceRowCount).Set("refreshed_at", request.Snapshot.RefreshedAt).Set("error_code", "").Where(ormbuilder.And(ormbuilder.Equal("id", request.Snapshot.ID), ormbuilder.Equal("status", request.ExpectedStatus))).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := s.executor(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return reportSnapshotRequireAffected(result)
}

func (s *ReportSnapshotStore) FailReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotFailRequest) error {
	if strings.TrimSpace(request.WorkspaceID) == "" {
		return fmt.Errorf("report snapshot workspace is required")
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "report_snapshots", request.WorkspaceID).Set("status", "failed").Set("error_code", request.ErrorCode).Where(ormbuilder.And(ormbuilder.Equal("id", request.ID), ormbuilder.Equal("status", request.ExpectedStatus))).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := s.executor(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return reportSnapshotRequireAffected(result)
}

type reportSnapshotExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *ReportSnapshotStore) executor(ctx context.Context) reportSnapshotExecutor {
	if tx := database.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return s.db
}

func (s *ReportSnapshotStore) LatestReportSnapshot(ctx context.Context, workspaceID, reportKey, scopeHash string) (reportmodel.ReportSnapshot, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "report_snapshots", workspaceID).Columns(reportSnapshotStoreColumns()...).Where(ormbuilder.And(ormbuilder.Equal("report_key", reportKey), ormbuilder.Equal("access_scope_hash", scopeHash), ormbuilder.Equal("status", "succeeded"))).OrderBy(ormbuilder.Descending("refreshed_at"), ormbuilder.Descending("id")).Limit(1).Build()
	if buildErr != nil {
		return reportmodel.ReportSnapshot{}, false, buildErr
	}
	return reportSnapshotScan(s.db.QueryRowContext(ctx, query, args...))
}

func (s *ReportSnapshotStore) reportSnapshotByIdempotency(ctx context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "report_snapshots", request.WorkspaceID).Columns(reportSnapshotStoreColumns()...).Where(ormbuilder.And(ormbuilder.Equal("report_key", request.ReportKey), ormbuilder.Equal("access_scope_hash", request.AccessScopeHash), ormbuilder.Equal("idempotency_key", request.IdempotencyKey))).Limit(1).Build()
	if buildErr != nil {
		return reportmodel.ReportSnapshot{}, false, buildErr
	}
	return reportSnapshotScan(s.db.QueryRowContext(ctx, query, args...))
}

func reportSnapshotStoreColumns() []string {
	return []string{"id", "workspace_id", "report_key", "access_scope_hash", "idempotency_key", "status", "summary_json", "watermark", "source_versions_json", "started_at", "refreshed_at", "error_code"}
}

func reportSnapshotScan(row *sql.Row) (reportmodel.ReportSnapshot, bool, error) {
	var snapshot reportmodel.ReportSnapshot
	var summaryJSON, versionsJSON string
	err := row.Scan(&snapshot.ID, &snapshot.WorkspaceID, &snapshot.ReportKey, &snapshot.AccessScopeHash, &snapshot.IdempotencyKey, &snapshot.Status, &summaryJSON, &snapshot.Watermark, &versionsJSON, &snapshot.StartedAt, &snapshot.RefreshedAt, &snapshot.ErrorCode)
	if err == sql.ErrNoRows {
		return reportmodel.ReportSnapshot{}, false, nil
	}
	if err != nil {
		return reportmodel.ReportSnapshot{}, false, err
	}
	if err := json.Unmarshal([]byte(summaryJSON), &snapshot.Summary); err != nil {
		return reportmodel.ReportSnapshot{}, false, err
	}
	if err := json.Unmarshal([]byte(versionsJSON), &snapshot.SourceVersions); err != nil {
		return reportmodel.ReportSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func reportSnapshotRequestValid(request reportcontract.ReportSnapshotBeginRequest) error {
	if strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.ReportKey) == "" || strings.TrimSpace(request.AccessScopeHash) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.StartedAt) == "" {
		return fmt.Errorf("report snapshot scope, idempotency key, and started_at are required")
	}
	return nil
}

func reportSnapshotID(request reportcontract.ReportSnapshotBeginRequest) string {
	sum := sha256.Sum256([]byte(request.WorkspaceID + "\x00" + request.ReportKey + "\x00" + request.AccessScopeHash + "\x00" + request.IdempotencyKey))
	return "rptsnap_" + hex.EncodeToString(sum[:16])
}

func reportSnapshotRequireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("report snapshot fencing conflict")
	}
	return nil
}
