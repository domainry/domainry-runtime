package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ reportcontract.ReportExportArtifactStore = (*ReportExportArtifactStore)(nil)

var scanReportExportArtifact = reportExportArtifactScan
var execReportExportArtifact = func(ctx context.Context, executor database.ActionExecutionExecutor, query string, args ...any) (sql.Result, error) {
	return executor.ExecContext(ctx, query, args...)
}

type ReportExportArtifactStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewReportExportArtifactStore(store *database.RuntimeStore) *ReportExportArtifactStore {
	return &ReportExportArtifactStore{store: store, db: store.DB()}
}

func (s *ReportExportArtifactStore) CreateOrGetReportExportArtifact(ctx context.Context, artifact reportmodel.ReportExportArtifact) (reportmodel.ReportExportArtifact, bool, error) {
	if err := reportExportArtifactValid(artifact); err != nil {
		return reportmodel.ReportExportArtifact{}, false, err
	}
	if current, ok, err := s.byIdempotency(ctx, artifact); err != nil {
		return reportmodel.ReportExportArtifact{}, false, err
	} else if ok {
		if !sameReportExportRequest(current, artifact) {
			return reportmodel.ReportExportArtifact{}, false, reportcontract.ErrReportExportIdempotencyConflict
		}
		return current, false, nil
	}
	artifact.ID = reportExportArtifactID(artifact)
	scopeJSON, _ := json.Marshal(artifact.Scope)
	columns := reportExportArtifactColumns()
	query := "INSERT INTO " + s.store.TableIdentifier("report_export_artifacts") + " (" + strings.Join(reportSnapshotQuoted(s.store, columns), ", ") + ") VALUES (" + strings.Join(reportSnapshotPlaceholders(s.store, len(columns)), ", ") + ")"
	_, err := execReportExportArtifact(ctx, s.executor(ctx), query,
		artifact.ID, artifact.WorkspaceID, artifact.ReportKey, artifact.ObjectKey, artifact.AuditID, artifact.BusinessDownloadID, artifact.RequesterUserID, artifact.RoleKey, artifact.IdempotencyKey,
		artifact.Token, artifact.Filename, string(scopeJSON), artifact.ScopeSHA256, artifact.AuthorizationScopeSHA256, artifact.ReportDefinitionSHA256, artifact.ControlDefinitionSHA256,
		base64.StdEncoding.EncodeToString(artifact.Content), artifact.ContentSHA256, artifact.RowCount, artifact.Watermarked, artifact.CreatedAt, artifact.ExpiresAt,
	)
	if err != nil {
		if current, ok, readErr := s.byIdempotency(ctx, artifact); readErr == nil && ok {
			return reconcileReportExportInsertConflict(current, artifact)
		}
		return reportmodel.ReportExportArtifact{}, false, err
	}
	return artifact, true, nil
}

func (s *ReportExportArtifactStore) ReportExportArtifactByToken(ctx context.Context, workspaceID, token string) (reportmodel.ReportExportArtifact, bool, error) {
	query := reportExportArtifactSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("token") + " = " + s.store.Placeholder(2)
	return scanReportExportArtifact(s.executor(ctx).QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(token)))
}

func (s *ReportExportArtifactStore) ReportExportArtifactByIdempotency(ctx context.Context, workspaceID, requesterUserID, reportKey, key string) (reportmodel.ReportExportArtifact, bool, error) {
	return s.byIdempotency(ctx, reportmodel.ReportExportArtifact{WorkspaceID: strings.TrimSpace(workspaceID), RequesterUserID: strings.TrimSpace(requesterUserID), ReportKey: strings.TrimSpace(reportKey), IdempotencyKey: strings.TrimSpace(key)})
}

func (s *ReportExportArtifactStore) LinkReportExportBusinessDownload(ctx context.Context, workspaceID, artifactID, businessDownloadID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	artifactID = strings.TrimSpace(artifactID)
	businessDownloadID = strings.TrimSpace(businessDownloadID)
	if len(workspaceID) == 0 || len(artifactID) == 0 || len(businessDownloadID) == 0 {
		return fmt.Errorf("report export artifact and business download identity are required")
	}
	query := "UPDATE " + s.store.TableIdentifier("report_export_artifacts") + " SET " + s.store.Identifier("business_download_id") + " = " + s.store.Placeholder(1) +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(3) +
		" AND " + s.store.Identifier("business_download_id") + " = ''"
	result, err := s.executor(ctx).ExecContext(ctx, query, businessDownloadID, workspaceID, artifactID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return nil
	}
	var current string
	lookup := "SELECT " + s.store.Identifier("business_download_id") + " FROM " + s.store.TableIdentifier("report_export_artifacts") +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	if err := s.executor(ctx).QueryRowContext(ctx, lookup, workspaceID, artifactID).Scan(&current); err != nil || current != businessDownloadID {
		return fmt.Errorf("report export artifact business download link is missing or conflicts")
	}
	return nil
}

func (s *ReportExportArtifactStore) byIdempotency(ctx context.Context, artifact reportmodel.ReportExportArtifact) (reportmodel.ReportExportArtifact, bool, error) {
	query := reportExportArtifactSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("requester_user_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("report_key") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(4)
	return scanReportExportArtifact(s.executor(ctx).QueryRowContext(ctx, query, artifact.WorkspaceID, artifact.RequesterUserID, artifact.ReportKey, artifact.IdempotencyKey))
}

func (s *ReportExportArtifactStore) executor(ctx context.Context) database.ActionExecutionExecutor {
	if transaction := database.ActionExecutionTransaction(ctx); transaction != nil {
		return transaction
	}
	return s.db
}

func reportExportArtifactColumns() []string {
	return []string{"id", "workspace_id", "report_key", "object_key", "audit_id", "business_download_id", "requester_user_id", "role_key", "idempotency_key", "token", "filename", "scope_json", "scope_sha256", "authorization_scope_sha256", "report_definition_sha256", "control_definition_sha256", "content_base64", "content_sha256", "row_count", "watermarked", "created_at", "expires_at"}
}

func reportExportArtifactSelect(store *database.RuntimeStore) string {
	return "SELECT " + strings.Join(reportSnapshotQuoted(store, reportExportArtifactColumns()), ", ") + " FROM " + store.TableIdentifier("report_export_artifacts")
}

func reportExportArtifactScan(row *sql.Row) (reportmodel.ReportExportArtifact, bool, error) {
	var artifact reportmodel.ReportExportArtifact
	var scopeJSON, contentBase64 string
	err := row.Scan(
		&artifact.ID, &artifact.WorkspaceID, &artifact.ReportKey, &artifact.ObjectKey, &artifact.AuditID, &artifact.BusinessDownloadID, &artifact.RequesterUserID, &artifact.RoleKey, &artifact.IdempotencyKey,
		&artifact.Token, &artifact.Filename, &scopeJSON, &artifact.ScopeSHA256, &artifact.AuthorizationScopeSHA256, &artifact.ReportDefinitionSHA256, &artifact.ControlDefinitionSHA256,
		&contentBase64, &artifact.ContentSHA256, &artifact.RowCount, &artifact.Watermarked, &artifact.CreatedAt, &artifact.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return reportmodel.ReportExportArtifact{}, false, nil
	}
	if err != nil {
		return reportmodel.ReportExportArtifact{}, false, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &artifact.Scope); err != nil {
		return reportmodel.ReportExportArtifact{}, false, err
	}
	artifact.Content, err = base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		return reportmodel.ReportExportArtifact{}, false, err
	}
	return artifact, true, nil
}

func reportExportArtifactValid(artifact reportmodel.ReportExportArtifact) error {
	if len(strings.TrimSpace(artifact.WorkspaceID)) == 0 {
		return fmt.Errorf("complete report export artifact identity, scope, and content are required")
	}
	if strings.TrimSpace(artifact.ReportKey) == "" || strings.TrimSpace(artifact.ObjectKey) == "" || strings.TrimSpace(artifact.AuditID) == "" || strings.TrimSpace(artifact.RequesterUserID) == "" || strings.TrimSpace(artifact.IdempotencyKey) == "" || strings.TrimSpace(artifact.Token) == "" || strings.TrimSpace(artifact.ScopeSHA256) == "" || strings.TrimSpace(artifact.ContentSHA256) == "" || artifact.RowCount < 0 || len(artifact.Content) == 0 {
		return fmt.Errorf("complete report export artifact identity, scope, and content are required")
	}
	return nil
}

func sameReportExportRequest(left, right reportmodel.ReportExportArtifact) bool {
	return left.ObjectKey == right.ObjectKey && left.ScopeSHA256 == right.ScopeSHA256 && left.AuthorizationScopeSHA256 == right.AuthorizationScopeSHA256 && left.ReportDefinitionSHA256 == right.ReportDefinitionSHA256 && left.ControlDefinitionSHA256 == right.ControlDefinitionSHA256
}

func reconcileReportExportInsertConflict(current, requested reportmodel.ReportExportArtifact) (reportmodel.ReportExportArtifact, bool, error) {
	if !sameReportExportRequest(current, requested) {
		return reportmodel.ReportExportArtifact{}, false, reportcontract.ErrReportExportIdempotencyConflict
	}
	return current, false, nil
}

func reportExportArtifactID(artifact reportmodel.ReportExportArtifact) string {
	sum := sha256.Sum256([]byte(artifact.WorkspaceID + "\x00" + artifact.RequesterUserID + "\x00" + artifact.ReportKey + "\x00" + artifact.IdempotencyKey))
	return "rptexp_" + hex.EncodeToString(sum[:16])
}
