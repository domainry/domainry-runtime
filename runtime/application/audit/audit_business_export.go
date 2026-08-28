package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	PermissionBusinessAuditExport = "audit.business.export"
	businessAuditExportTTL        = 15 * time.Minute
	businessAuditExportMaxRows    = 1000
)

var auditExportFilterValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}$`)

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
		service.auditBusinessExportOutcome(ctx, "business_audit_export_prepare_denied", principal, "", "permission_denied")
		return BusinessAuditExportPrepared{}, err
	}
	if service == nil || service.exportStore == nil || len(service.exportTokenKey) < 16 {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindInternal, "backend.audit.export_unavailable", nil)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindBadRequest, "backend.idempotency.key_required", nil)
	}
	now := service.businessExportNow()
	filters, err := normalizeBusinessAuditExportFilters(request, principal, now)
	if err != nil {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_prepare_denied", principal, "", apperror.CodeOf(err))
		return BusinessAuditExportPrepared{}, err
	}
	if service.authorizeExportScope != nil {
		if err := service.authorizeExportScope(ctx, filters, principal); err != nil {
			service.auditBusinessExportOutcome(ctx, "business_audit_export_prepare_denied", principal, "", "data_scope_denied")
			return BusinessAuditExportPrepared{}, err
		}
	}
	query := auditmodel.AuditEventQuery{
		Event: filters.Event, ObjectKey: filters.ObjectKey, RecordID: filters.RecordID, ActorID: filters.ActorID,
		RoleKey: filters.RoleKey, CreatedFrom: filters.CreatedFrom, CreatedTo: filters.CreatedTo,
		Class: auditmodel.AuditEventClassBusiness, Limit: businessAuditExportMaxRows,
	}
	events, err := service.surfaceEvents(ctx, query, principal)
	if err != nil {
		return BusinessAuditExportPrepared{}, err
	}
	rows := make([][]string, 0, len(events))
	for _, event := range events {
		if auditmodel.ClassifyAuditEvent(event) != auditmodel.AuditEventClassBusiness {
			continue
		}
		result := businessAuditEventResult(event)
		if filters.Result != "" && !strings.EqualFold(filters.Result, result) {
			continue
		}
		rows = append(rows, []string{event.ID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, result, event.CreatedAt})
	}
	if len(rows) == 0 {
		err := businessAuditExportError(apperror.KindBadRequest, "backend.audit.export_no_results", nil)
		service.auditBusinessExportOutcome(ctx, "business_audit_export_prepare_denied", principal, "", "zero_results")
		return BusinessAuditExportPrepared{}, err
	}
	content, err := encodeBusinessAuditCSV(rows)
	if err != nil {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindInternal, "backend.audit.export_encode_failed", err)
	}
	scopeHash := auditExportHash(filters)
	authorizationHash := businessAuditAuthorizationHash(principal)
	now = now.UTC()
	expiresAt := now.Add(businessAuditExportTTL).Format(time.RFC3339Nano)
	artifactID := businessAuditExportArtifactID(principal.WorkspaceID, principal.UserID, idempotencyKey)
	auditIdentity := "business_audit_events:" + artifactID + ":sha256:" + scopeHash
	token := service.businessAuditExportToken(artifactID, scopeHash, expiresAt)
	artifact := auditmodel.AuditBusinessExportArtifact{
		ID: artifactID, WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID), RoleKey: principal.RoleKey,
		IdempotencyKey: idempotencyKey, Filters: filters, ScopeSHA256: scopeHash, AuthorizationScopeSHA256: authorizationHash,
		TokenSHA256: auditExportHash(token), Filename: "business-audit-events-" + now.Format("20060102T150405Z") + ".csv",
		ContentSHA256: auditExportBytesHash(content), RowCount: len(rows), Content: content, AuditIdentity: auditIdentity,
		Status: "prepared", CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt,
	}
	stored, created, err := service.exportStore.CreateOrGetBusinessAuditExport(ctx, artifact)
	if errors.Is(err, auditcontract.ErrBusinessAuditExportIdempotencyConflict) {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindConflict, "backend.idempotency.key_conflict", err)
	}
	if err != nil {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindInternal, "backend.audit.export_persistence_failed", err)
	}
	token = service.businessAuditExportToken(stored.ID, stored.ScopeSHA256, stored.ExpiresAt)
	if auditExportHash(token) != stored.TokenSHA256 {
		return BusinessAuditExportPrepared{}, businessAuditExportError(apperror.KindForbidden, "backend.audit.export_integrity_failed", nil)
	}
	if err := service.appendBusinessExportAudit(ctx, "business_audit_export_prepared", principal, stored, map[string]any{"idempotency_replayed": !created}); err != nil {
		return BusinessAuditExportPrepared{}, err
	}
	return businessAuditExportPrepared(stored, token), nil
}

func (service *AuditApplicationService) DownloadBusinessEventExport(ctx context.Context, token string, principal principalmodel.Principal) ([]byte, string, error) {
	if err := requireBusinessAuditExport(principal); err != nil {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_download_denied", principal, "", "permission_denied")
		return nil, "", err
	}
	if service == nil || service.exportStore == nil || len(service.exportTokenKey) < 16 {
		return nil, "", businessAuditExportError(apperror.KindInternal, "backend.audit.export_unavailable", nil)
	}
	token = strings.TrimSpace(token)
	if len(token) < 80 || len(token) > 160 {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_download_denied", principal, "", "not_found")
		return nil, "", businessAuditExportError(apperror.KindNotFound, "backend.audit.export_download_not_found", nil)
	}
	artifact, found, err := service.exportStore.BusinessAuditExportByTokenHash(ctx, principal.WorkspaceID, auditExportHash(token))
	if err != nil {
		return nil, "", businessAuditExportError(apperror.KindInternal, "backend.audit.export_persistence_failed", err)
	}
	if !found || !hmac.Equal([]byte(token), []byte(service.businessAuditExportToken(artifact.ID, artifact.ScopeSHA256, artifact.ExpiresAt))) {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_download_denied", principal, "", "not_found")
		return nil, "", businessAuditExportError(apperror.KindNotFound, "backend.audit.export_download_not_found", nil)
	}
	deny := func(code, reason string) ([]byte, string, error) {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_download_denied", principal, artifact.ID, reason)
		return nil, "", businessAuditExportError(apperror.KindForbidden, code, nil)
	}
	if artifact.RequesterUserID != strings.TrimSpace(principal.UserID) || artifact.WorkspaceID != strings.TrimSpace(principal.WorkspaceID) {
		return deny("backend.audit.export_requester_mismatch", "requester_or_workspace_mismatch")
	}
	expiresAt, parseErr := time.Parse(time.RFC3339Nano, artifact.ExpiresAt)
	if parseErr != nil || !service.businessExportNow().UTC().Before(expiresAt) {
		service.auditBusinessExportOutcome(ctx, "business_audit_export_download_expired", principal, artifact.ID, "expired")
		return nil, "", businessAuditExportError(apperror.KindForbidden, "backend.audit.export_download_expired", nil)
	}
	if businessAuditAuthorizationHash(principal) != artifact.AuthorizationScopeSHA256 {
		return deny("backend.audit.export_scope_changed", "authorization_scope_changed")
	}
	if service.authorizeExportScope != nil {
		if err := service.authorizeExportScope(ctx, artifact.Filters, principal); err != nil {
			return deny("backend.audit.export_scope_changed", "data_scope_denied")
		}
	}
	if auditExportBytesHash(artifact.Content) != artifact.ContentSHA256 || auditExportHash(artifact.Filters) != artifact.ScopeSHA256 {
		return deny("backend.audit.export_integrity_failed", "content_or_scope_hash_mismatch")
	}
	now := service.businessExportNow().UTC().Format(time.RFC3339Nano)
	firstDownload, err := service.exportStore.RecordBusinessAuditExportDownload(ctx, artifact.WorkspaceID, artifact.ID, now)
	if err != nil {
		return nil, "", businessAuditExportError(apperror.KindInternal, "backend.audit.export_persistence_failed", err)
	}
	if firstDownload {
		if err := service.appendBusinessExportAudit(ctx, "business_audit_export_downloaded", principal, artifact, nil); err != nil {
			return nil, "", err
		}
	}
	return append([]byte(nil), artifact.Content...), artifact.Filename, nil
}

func normalizeBusinessAuditExportFilters(request auditmodel.AuditBusinessExportRequest, principal principalmodel.Principal, now time.Time) (auditmodel.AuditBusinessExportFilter, error) {
	if format := strings.ToLower(strings.TrimSpace(request.Format)); format != "" && format != "csv" {
		return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindBadRequest, "backend.audit.export_format_invalid", nil)
	}
	filters := request.Filters
	values := []*string{&filters.Event, &filters.ObjectKey, &filters.RecordID, &filters.ActorID, &filters.RoleKey, &filters.Result}
	for _, value := range values {
		*value = strings.TrimSpace(*value)
		if *value != "" && !auditExportFilterValue.MatchString(*value) {
			return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindBadRequest, "backend.audit.export_filter_invalid", nil)
		}
	}
	filters.Result = strings.ToLower(filters.Result)
	for _, value := range []*string{&filters.CreatedFrom, &filters.CreatedTo} {
		*value = strings.TrimSpace(*value)
		if *value != "" {
			parsed, err := time.Parse(time.RFC3339, *value)
			if err != nil {
				return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindBadRequest, "backend.audit.export_time_range_invalid", err)
			}
			*value = parsed.UTC().Format(time.RFC3339)
		}
	}
	if filters.CreatedFrom != "" && filters.CreatedTo != "" && filters.CreatedFrom > filters.CreatedTo {
		return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindBadRequest, "backend.audit.export_time_range_invalid", nil)
	}
	cutoff := now.UTC().AddDate(0, 0, -businessAuditRetentionDays).Format(time.RFC3339)
	if filters.CreatedFrom == "" || filters.CreatedFrom < cutoff {
		filters.CreatedFrom = cutoff
	}
	if filters.ObjectKey == "" || filters.RecordID == "" {
		if filters.ActorID != "" && filters.ActorID != principal.UserID {
			return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindForbidden, "backend.audit.export_actor_scope_denied", nil)
		}
		filters.ActorID = principal.UserID
		if filters.RoleKey != "" && filters.RoleKey != principal.RoleKey {
			return auditmodel.AuditBusinessExportFilter{}, businessAuditExportError(apperror.KindForbidden, "backend.audit.export_role_scope_denied", nil)
		}
	}
	return filters, nil
}

func (service *AuditApplicationService) businessExportNow() time.Time {
	if service != nil && service.clock != nil {
		return service.clock()
	}
	return time.Now()
}

func businessAuditEventResult(event auditmodel.AuditEvent) string {
	for _, key := range []string{"result", "outcome", "status"} {
		value, ok := event.Metadata[key]
		if !ok {
			continue
		}
		result := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if auditExportFilterValue.MatchString(result) {
			return result
		}
	}
	lower := strings.ToLower(event.Event)
	for _, result := range []string{"denied", "expired", "failed", "rejected", "cancelled", "succeeded", "completed"} {
		if strings.Contains(lower, result) {
			return result
		}
	}
	return ""
}

func encodeBusinessAuditCSV(rows [][]string) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"audit_id", "event", "object_key", "record_id", "actor_id", "result", "created_at"}); err != nil {
		return nil, err
	}
	if err := writer.WriteAll(rows); err != nil {
		return nil, err
	}
	writer.Flush()
	return buffer.Bytes(), writer.Error()
}

func requireBusinessAuditExport(principal principalmodel.Principal) error {
	if err := requireAuditPermission(principal, PermissionBusinessAuditExport, false); err != nil {
		return err
	}
	return requireAuditPermission(principal, PermissionBusinessAuditRead, false)
}

func (service *AuditApplicationService) businessAuditExportToken(id, scopeHash, expiresAt string) string {
	payload := id + "." + expiresAt
	mac := hmac.New(sha256.New, service.exportTokenKey)
	_, _ = mac.Write([]byte(payload + "\x00" + scopeHash))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func businessAuditExportArtifactID(workspaceID, userID, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(userID) + "\x00business_audit_events\x00" + strings.TrimSpace(idempotencyKey)))
	return "audexp_" + hex.EncodeToString(sum[:16])
}

func businessAuditAuthorizationHash(principal principalmodel.Principal) string {
	systemCapabilities := append([]string(nil), principal.SystemCapabilities...)
	sort.Strings(systemCapabilities)
	return auditExportHash(struct {
		WorkspaceID           string   `json:"workspace_id"`
		UserID                string   `json:"user_id"`
		RoleKey               string   `json:"role_key"`
		AuthorizationRevision string   `json:"authorization_revision"`
		SystemScope           string   `json:"system_scope,omitempty"`
		SystemCapabilities    []string `json:"system_capabilities,omitempty"`
	}{
		strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(principal.UserID), strings.TrimSpace(principal.RoleKey),
		strings.TrimSpace(principal.AuthorizationRevision), string(principal.SystemScope.Kind), systemCapabilities,
	})
}

func auditExportHash(value any) string {
	encoded, _ := json.Marshal(value)
	return auditExportBytesHash(encoded)
}

func auditExportBytesHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (service *AuditApplicationService) appendBusinessExportAudit(ctx context.Context, event string, principal principalmodel.Principal, artifact auditmodel.AuditBusinessExportArtifact, extra map[string]any) error {
	metadata := map[string]any{
		"artifact_id": artifact.ID, "audit_identity": artifact.AuditIdentity, "content_sha256": artifact.ContentSHA256,
		"scope_sha256": artifact.ScopeSHA256, "row_count": artifact.RowCount, "expires_at": artifact.ExpiresAt,
	}
	for key, value := range extra {
		metadata[key] = value
	}
	err := service.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: event, ObjectKey: "business_audit_events", RecordID: artifact.ID, Principal: principal, Summary: "Business audit event export lifecycle", Metadata: metadata})
	if err != nil {
		return businessAuditExportError(apperror.KindInternal, "backend.audit.export_audit_failed", err)
	}
	return nil
}

func (service *AuditApplicationService) auditBusinessExportOutcome(ctx context.Context, event string, principal principalmodel.Principal, artifactID, reason string) {
	if service == nil || service.AuditDomainService == nil || !principal.Known || strings.TrimSpace(principal.WorkspaceID) == "" {
		return
	}
	_ = service.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: event, ObjectKey: "business_audit_events", RecordID: artifactID, Principal: principal, Summary: "Business audit event export rejected", Metadata: map[string]any{"reason": reason}})
}

func businessAuditExportPrepared(artifact auditmodel.AuditBusinessExportArtifact, token string) BusinessAuditExportPrepared {
	return BusinessAuditExportPrepared{ID: artifact.ID, ReportSource: "business_audit_events", Filename: artifact.Filename,
		ContentSHA256: artifact.ContentSHA256, RowCount: artifact.RowCount, AuditIdentity: artifact.AuditIdentity,
		ScopeSHA256: artifact.ScopeSHA256, Filters: artifact.Filters, DownloadToken: token, ExpiresAt: artifact.ExpiresAt}
}

func businessAuditExportError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
