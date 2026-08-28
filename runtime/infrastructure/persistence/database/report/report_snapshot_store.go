package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

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
		query := "UPDATE " + s.store.TableIdentifier("report_snapshots") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("started_at") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("error_code") + " = '' WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(3)
		if _, err := s.db.ExecContext(ctx, query, "refreshing", request.StartedAt, current.ID); err != nil {
			return reportmodel.ReportSnapshot{}, false, err
		}
		current.Status, current.StartedAt, current.ErrorCode = "refreshing", request.StartedAt, ""
		return current, true, nil
	}
	id := reportSnapshotID(request)
	columns := []string{"id", "workspace_id", "report_key", "access_scope_hash", "idempotency_key", "status", "summary_json", "watermark", "source_versions_json", "row_count", "source_row_count", "started_at", "refreshed_at", "error_code"}
	query := "INSERT INTO " + s.store.TableIdentifier("report_snapshots") + " (" + strings.Join(reportSnapshotQuoted(s.store, columns), ", ") + ") VALUES (" + strings.Join(reportSnapshotPlaceholders(s.store, len(columns)), ", ") + ")"
	_, err := s.db.ExecContext(ctx, query, id, request.WorkspaceID, request.ReportKey, request.AccessScopeHash, request.IdempotencyKey, "refreshing", "{}", "", "{}", 0, 0, request.StartedAt, "", "")
	if err != nil {
		if current, ok, readErr := s.reportSnapshotByIdempotency(ctx, request); readErr == nil && ok {
			return current, current.Status != "succeeded", nil
		}
		return reportmodel.ReportSnapshot{}, false, err
	}
	return reportmodel.ReportSnapshot{ID: id, WorkspaceID: request.WorkspaceID, ReportKey: request.ReportKey, AccessScopeHash: request.AccessScopeHash, IdempotencyKey: request.IdempotencyKey, Status: "refreshing", StartedAt: request.StartedAt}, true, nil
}

func (s *ReportSnapshotStore) CompleteReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotCompleteRequest) error {
	// ReportSummary and SourceVersions contain only JSON-safe concrete fields.
	summaryJSON, _ := json.Marshal(request.Snapshot.Summary)
	versionsJSON, _ := json.Marshal(request.Snapshot.SourceVersions)
	query := "UPDATE " + s.store.TableIdentifier("report_snapshots") + " SET " + s.store.Identifier("status") + " = 'succeeded', " + s.store.Identifier("summary_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("watermark") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("source_versions_json") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("row_count") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("source_row_count") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("refreshed_at") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("error_code") + " = '' WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(8)
	result, err := s.executor(ctx).ExecContext(ctx, query, string(summaryJSON), request.Snapshot.Watermark, string(versionsJSON), request.Snapshot.Summary.RowCount, request.Snapshot.Summary.SourceRowCount, request.Snapshot.RefreshedAt, request.Snapshot.ID, request.ExpectedStatus)
	if err != nil {
		return err
	}
	return reportSnapshotRequireAffected(result)
}

func (s *ReportSnapshotStore) FailReportSnapshot(ctx context.Context, id, expectedStatus, code string) error {
	query := "UPDATE " + s.store.TableIdentifier("report_snapshots") + " SET " + s.store.Identifier("status") + " = 'failed', " + s.store.Identifier("error_code") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(3)
	result, err := s.executor(ctx).ExecContext(ctx, query, code, id, expectedStatus)
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
	query := reportSnapshotSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("report_key") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("access_scope_hash") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("status") + " = 'succeeded' ORDER BY " + s.store.Identifier("refreshed_at") + " DESC, " + s.store.Identifier("id") + " DESC"
	return reportSnapshotScan(s.db.QueryRowContext(ctx, query, workspaceID, reportKey, scopeHash))
}

func (s *ReportSnapshotStore) reportSnapshotByIdempotency(ctx context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	query := reportSnapshotSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("report_key") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("access_scope_hash") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(4)
	return reportSnapshotScan(s.db.QueryRowContext(ctx, query, request.WorkspaceID, request.ReportKey, request.AccessScopeHash, request.IdempotencyKey))
}

func reportSnapshotSelect(store *database.RuntimeStore) string {
	return "SELECT " + strings.Join(reportSnapshotQuoted(store, []string{"id", "workspace_id", "report_key", "access_scope_hash", "idempotency_key", "status", "summary_json", "watermark", "source_versions_json", "started_at", "refreshed_at", "error_code"}), ", ") + " FROM " + store.TableIdentifier("report_snapshots")
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

func reportSnapshotQuoted(store *database.RuntimeStore, columns []string) []string {
	result := make([]string, len(columns))
	for index, column := range columns {
		result[index] = store.Identifier(column)
	}
	return result
}

func reportSnapshotPlaceholders(store *database.RuntimeStore, count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = store.Placeholder(index + 1)
	}
	return result
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
