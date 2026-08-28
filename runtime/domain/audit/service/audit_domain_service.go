package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
)

// AuditDomainService owns audit-domain behavior.
type AuditDomainService struct {
	repository auditrepository.AuditRepository
}

func NewAuditDomainService(repository auditrepository.AuditRepository) *AuditDomainService {
	return &AuditDomainService{repository: repository}
}

func (s *AuditDomainService) Events(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	if !principal.Known {
		return nil, auditDomainError(apperror.KindForbidden, "backend.role.unknown", nil)
	}
	if !principal.HasPermission("identity.audit.view") && !principal.Allows("identity_permission", "read") {
		return nil, auditDomainError(apperror.KindForbidden, "backend.audit.view_permission_required", nil)
	}
	events, err := s.repository.ListAuditEvents(ctx, principalWorkspaceID(principal), query)
	if err != nil {
		return nil, auditDomainError(apperror.KindInternal, "backend.internal", err)
	}
	return events, nil
}

func (s *AuditDomainService) Options(ctx context.Context, query auditmodel.AuditOptionQuery, principal principalmodel.Principal) ([]auditmodel.AuditOption, error) {
	if !principal.Known {
		return nil, auditDomainError(apperror.KindForbidden, "backend.role.unknown", nil)
	}
	if !principal.HasPermission("identity.audit.view") && !principal.Allows("identity_permission", "read") {
		return nil, auditDomainError(apperror.KindForbidden, "backend.audit.view_permission_required", nil)
	}
	switch strings.TrimSpace(query.Field) {
	case "record_id", "actor_id", "role_key", "event":
	default:
		return nil, auditDomainError(apperror.KindBadRequest, "backend.audit.option_field_invalid", nil)
	}
	options, err := s.repository.ListAuditOptions(ctx, principalWorkspaceID(principal), query)
	if err != nil {
		return nil, auditDomainError(apperror.KindInternal, "backend.internal", err)
	}
	return options, nil
}

func (s *AuditDomainService) AppendAudit(ctx context.Context, request auditcontract.AuditAppendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.repository == nil {
		return auditDomainError(apperror.KindInternal, "backend.audit.repository_unavailable", nil)
	}
	event := s.NewAuditEvent(ctx, request)
	if err := s.repository.InsertAuditEvent(ctx, event.WorkspaceID, event); err != nil {
		return auditDomainError(apperror.KindInternal, "backend.internal", err)
	}
	return nil
}

func (s *AuditDomainService) AppendAuditTelemetry(ctx context.Context, request auditcontract.AuditAppendRequest) {
	_ = s.AppendAudit(ctx, request)
}

func (s *AuditDomainService) ListAuditEvents(ctx context.Context, workspaceID string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil {
		return nil, auditDomainError(apperror.KindInternal, "backend.audit.repository_unavailable", nil)
	}
	return s.repository.ListAuditEvents(ctx, workspaceID, query)
}

func (s *AuditDomainService) ListAuditEventsForSystem(ctx context.Context, scope principalmodel.SystemScope, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil {
		return nil, auditDomainError(apperror.KindInternal, "backend.audit.repository_unavailable", nil)
	}
	return s.repository.ListAuditEventsForSystem(ctx, scope, query)
}

func (s *AuditDomainService) ListAuditOptions(ctx context.Context, workspaceID string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil {
		return nil, auditDomainError(apperror.KindInternal, "backend.audit.repository_unavailable", nil)
	}
	return s.repository.ListAuditOptions(ctx, workspaceID, query)
}

// AppendWithMetadata is best-effort operational telemetry. Mandatory domain
// audit must still be written through the owning transaction or unit of work.
func (s *AuditDomainService) AppendWithMetadata(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
	s.AppendAuditTelemetry(ctx, auditcontract.AuditAppendRequest{
		Event: event, ObjectKey: objectKey, RecordID: recordID, Principal: principal,
		Summary: summary, Before: before, After: after, Metadata: metadata,
	})
}

var _ auditcontract.AuditAppender = (*AuditDomainService)(nil)
var _ auditcontract.AuditTelemetryAppender = (*AuditDomainService)(nil)
var _ auditcontract.AuditEventFactory = (*AuditDomainService)(nil)
var _ auditcontract.AuditReader = (*AuditDomainService)(nil)

func auditDomainError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
