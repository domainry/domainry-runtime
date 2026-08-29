package audit

import (
	"context"
	"errors"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const PermissionBusinessAuditExport = "audit.business.export"
const businessAuditExportTTL = 15 * time.Minute // compatibility for transport-level expiry assertions

// BusinessAuditExportPrepared is the Runtime transport DTO. Export behavior
// and artifact identity are owned by domainry-audit.
type BusinessAuditExportPrepared struct {
	ID            string                               `json:"id"`
	ReportSource  string                               `json:"report_source"`
	Filename      string                               `json:"filename"`
	ContentSHA256 string                               `json:"content_sha256"`
	RowCount      int                                  `json:"row_count"`
	AuditIdentity string                               `json:"audit_identity"`
	ScopeSHA256   string                               `json:"scope_sha256"`
	Filters       auditmodel.AuditBusinessExportFilter `json:"filters"`
	DownloadToken string                               `json:"download_token"`
	ExpiresAt     string                               `json:"expires_at"`
}

func (service *AuditApplicationService) PrepareBusinessEventExport(ctx context.Context, request auditmodel.AuditBusinessExportRequest, idempotencyKey string, principal principalmodel.Principal) (BusinessAuditExportPrepared, error) {
	if err := requireBusinessAuditExport(principal); err != nil {
		return BusinessAuditExportPrepared{}, err
	}
	if service == nil || service.exporter == nil {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindInternal, "backend.audit.export_unavailable", nil)
	}
	prepared, err := service.exporter.PrepareExport(ctx, request, idempotencyKey, exportPrincipal(principal))
	if err != nil {
		return BusinessAuditExportPrepared{}, runtimeExportError(err)
	}
	return BusinessAuditExportPrepared{ID: prepared.ID, ReportSource: prepared.ReportSource, Filename: prepared.Filename, ContentSHA256: prepared.ContentSHA256, RowCount: prepared.RowCount, AuditIdentity: prepared.AuditIdentity, ScopeSHA256: prepared.ScopeSHA256, Filters: prepared.Filters, DownloadToken: prepared.DownloadToken, ExpiresAt: prepared.ExpiresAt}, nil
}

func (service *AuditApplicationService) DownloadBusinessEventExport(ctx context.Context, token string, principal principalmodel.Principal) ([]byte, string, error) {
	if err := requireBusinessAuditExport(principal); err != nil {
		return nil, "", err
	}
	if service == nil || service.exporter == nil {
		return nil, "", businessAuditExportError(apperror.KindInternal, "backend.audit.export_unavailable", nil)
	}
	content, filename, err := service.exporter.DownloadExport(ctx, token, exportPrincipal(principal))
	if err != nil {
		return nil, "", runtimeExportError(err)
	}
	return content, filename, nil
}

func requireBusinessAuditExport(principal principalmodel.Principal) error {
	if err := requireAuditPermission(principal, PermissionBusinessAuditExport, false); err != nil {
		return err
	}
	return requireAuditPermission(principal, PermissionBusinessAuditRead, false)
}
func exportPrincipal(p principalmodel.Principal) auditmodel.ExportPrincipal {
	return auditmodel.ExportPrincipal{WorkspaceID: p.WorkspaceID, UserID: p.UserID, RoleKey: p.RoleKey, AuthorizationRevision: p.AuthorizationRevision, SystemScope: string(p.SystemScope.Kind), SystemCapabilities: append([]string(nil), p.SystemCapabilities...), AuthorizationContext: p}
}
func runtimeExportError(err error) error {
	var exported *auditmodel.ExportError
	if !errors.As(err, &exported) {
		return err
	}
	kind := apperror.KindBadRequest
	code := "backend.audit." + exported.Code
	switch exported.Code {
	case "export_unavailable", "export_encode_failed", "export_persistence_failed", "export_audit_failed":
		kind = apperror.KindInternal
	case "idempotency_key_conflict":
		kind = apperror.KindConflict
		code = "backend.idempotency.key_conflict"
	case "idempotency_key_required":
		code = "backend.idempotency.key_required"
	case "export_download_not_found":
		kind = apperror.KindNotFound
	case "export_requester_mismatch", "export_download_expired", "export_scope_changed", "export_integrity_failed", "export_actor_scope_denied", "export_role_scope_denied":
		kind = apperror.KindForbidden
	}
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
func businessAuditExportError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
