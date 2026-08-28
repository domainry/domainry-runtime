package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ auditcontract.AuditBusinessExportStore = (*AuditBusinessExportStore)(nil)

type AuditBusinessExportStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewAuditBusinessExportStore(store *database.RuntimeStore) *AuditBusinessExportStore {
	return &AuditBusinessExportStore{store: store, db: store.DB()}
}

func (s *AuditBusinessExportStore) CreateOrGetBusinessAuditExport(ctx context.Context, artifact auditmodel.AuditBusinessExportArtifact) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	if err := validBusinessAuditExport(artifact); err != nil {
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	}
	if current, found, err := s.byIdempotency(ctx, artifact); err != nil {
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	} else if found {
		if !sameBusinessAuditExportRequest(current, artifact) {
			return auditmodel.AuditBusinessExportArtifact{}, false, auditcontract.ErrBusinessAuditExportIdempotencyConflict
		}
		return current, false, nil
	}
	artifact.ID = businessAuditExportID(artifact)
	filtersJSON, _ := json.Marshal(artifact.Filters)
	columns := businessAuditExportColumns()
	query := "INSERT INTO " + s.store.TableIdentifier("business_audit_export_artifacts") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	_, err := s.db.ExecContext(ctx, query,
		artifact.ID, artifact.WorkspaceID, artifact.RequesterUserID, artifact.RoleKey, artifact.IdempotencyKey,
		string(filtersJSON), artifact.ScopeSHA256, artifact.AuthorizationScopeSHA256, artifact.TokenSHA256,
		artifact.Filename, artifact.ContentSHA256, artifact.RowCount, base64.StdEncoding.EncodeToString(artifact.Content),
		artifact.AuditIdentity, artifact.Status, artifact.CreatedAt, artifact.ExpiresAt, artifact.DownloadCount, artifact.LastDownloadedAt,
	)
	if err != nil {
		if current, found, readErr := s.byIdempotency(ctx, artifact); readErr == nil && found {
			if sameBusinessAuditExportRequest(current, artifact) {
				return current, false, nil
			}
			return auditmodel.AuditBusinessExportArtifact{}, false, auditcontract.ErrBusinessAuditExportIdempotencyConflict
		}
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	}
	return artifact, true, nil
}

func (s *AuditBusinessExportStore) BusinessAuditExportByTokenHash(ctx context.Context, workspaceID, tokenHash string) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	query := businessAuditExportSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("token_sha256") + " = " + s.store.Placeholder(2)
	return scanBusinessAuditExport(s.db.QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(tokenHash)))
}

func (s *AuditBusinessExportStore) RecordBusinessAuditExportDownload(ctx context.Context, workspaceID, artifactID, downloadedAt string) (bool, error) {
	query := "UPDATE " + s.store.TableIdentifier("business_audit_export_artifacts") + " SET " +
		s.store.Identifier("download_count") + " = 1, " +
		s.store.Identifier("last_downloaded_at") + " = " + s.store.Placeholder(1) + ", " +
		s.store.Identifier("status") + " = " + s.store.Placeholder(2) + " WHERE " +
		s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(4) +
		" AND " + s.store.Identifier("download_count") + " = 0"
	result, err := s.db.ExecContext(ctx, query, strings.TrimSpace(downloadedAt), "downloaded", strings.TrimSpace(workspaceID), strings.TrimSpace(artifactID))
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return true, nil
	}
	var count int
	lookup := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("business_audit_export_artifacts") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	if err := s.db.QueryRowContext(ctx, lookup, strings.TrimSpace(workspaceID), strings.TrimSpace(artifactID)).Scan(&count); err != nil {
		return false, err
	}
	if count != 1 {
		return false, fmt.Errorf("business audit export artifact not found")
	}
	return false, nil
}

func (s *AuditBusinessExportStore) byIdempotency(ctx context.Context, artifact auditmodel.AuditBusinessExportArtifact) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	query := businessAuditExportSelect(s.store) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("requester_user_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(3)
	return scanBusinessAuditExport(s.db.QueryRowContext(ctx, query, artifact.WorkspaceID, artifact.RequesterUserID, artifact.IdempotencyKey))
}

func businessAuditExportColumns() []string {
	return []string{"id", "workspace_id", "requester_user_id", "role_key", "idempotency_key", "filters_json", "scope_sha256", "authorization_scope_sha256", "token_sha256", "filename", "content_sha256", "row_count", "content_base64", "audit_identity", "status", "created_at", "expires_at", "download_count", "last_downloaded_at"}
}

func businessAuditExportSelect(store *database.RuntimeStore) string {
	return "SELECT " + strings.Join(quotedColumns(store, businessAuditExportColumns()), ", ") + " FROM " + store.TableIdentifier("business_audit_export_artifacts")
}

func scanBusinessAuditExport(row *sql.Row) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	var artifact auditmodel.AuditBusinessExportArtifact
	var filtersJSON, contentBase64 string
	err := row.Scan(&artifact.ID, &artifact.WorkspaceID, &artifact.RequesterUserID, &artifact.RoleKey, &artifact.IdempotencyKey,
		&filtersJSON, &artifact.ScopeSHA256, &artifact.AuthorizationScopeSHA256, &artifact.TokenSHA256,
		&artifact.Filename, &artifact.ContentSHA256, &artifact.RowCount, &contentBase64, &artifact.AuditIdentity,
		&artifact.Status, &artifact.CreatedAt, &artifact.ExpiresAt, &artifact.DownloadCount, &artifact.LastDownloadedAt)
	if err == sql.ErrNoRows {
		return auditmodel.AuditBusinessExportArtifact{}, false, nil
	}
	if err != nil {
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	}
	if err := json.Unmarshal([]byte(filtersJSON), &artifact.Filters); err != nil {
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	}
	artifact.Content, err = base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		return auditmodel.AuditBusinessExportArtifact{}, false, err
	}
	return artifact, true, nil
}

func validBusinessAuditExport(artifact auditmodel.AuditBusinessExportArtifact) error {
	if strings.TrimSpace(artifact.WorkspaceID) == "" || strings.TrimSpace(artifact.RequesterUserID) == "" ||
		strings.TrimSpace(artifact.IdempotencyKey) == "" || strings.TrimSpace(artifact.ScopeSHA256) == "" ||
		strings.TrimSpace(artifact.AuthorizationScopeSHA256) == "" || strings.TrimSpace(artifact.TokenSHA256) == "" ||
		strings.TrimSpace(artifact.ContentSHA256) == "" || strings.TrimSpace(artifact.AuditIdentity) == "" ||
		artifact.RowCount < 1 || len(artifact.Content) == 0 {
		return fmt.Errorf("complete business audit export identity, scope, and content are required")
	}
	return nil
}

func sameBusinessAuditExportRequest(left, right auditmodel.AuditBusinessExportArtifact) bool {
	return left.ScopeSHA256 == right.ScopeSHA256 && left.AuthorizationScopeSHA256 == right.AuthorizationScopeSHA256
}

func businessAuditExportID(artifact auditmodel.AuditBusinessExportArtifact) string {
	sum := sha256.Sum256([]byte(artifact.WorkspaceID + "\x00" + artifact.RequesterUserID + "\x00business_audit_events\x00" + artifact.IdempotencyKey))
	return "audexp_" + hex.EncodeToString(sum[:16])
}
