package audit

import (
	"context"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	auditservice "github.com/domainry/domainry-runtime/runtime/domain/audit/service"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AuditApplicationService struct {
	*auditservice.AuditDomainService
	projectEvents func(context.Context, []auditmodel.AuditEvent, principalmodel.Principal) ([]auditmodel.AuditEvent, error)
	exporter      auditmodel.Exporter
}

func NewAuditApplicationService(repository auditrepository.AuditRepository, exporters ...auditmodel.Exporter) *AuditApplicationService {
	service := &AuditApplicationService{AuditDomainService: auditservice.NewAuditDomainService(repository)}
	if len(exporters) > 0 {
		service.exporter = exporters[0]
	}
	return service
}

func (service *AuditApplicationService) ConfigureBusinessExport(tokenKey []byte, authorizer func(context.Context, auditmodel.AuditBusinessExportFilter, principalmodel.Principal) error) {
	if service != nil {
		if service.exporter != nil {
			service.exporter.ConfigureExport(tokenKey, func(ctx context.Context, filters auditmodel.AuditBusinessExportFilter, actor auditmodel.ExportPrincipal) error {
				if authorizer == nil {
					return nil
				}
				principal, ok := actor.AuthorizationContext.(principalmodel.Principal)
				if !ok {
					return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.audit.export_scope_changed"}
				}
				return authorizer(ctx, filters, principal)
			})
		}
	}
}

func (service *AuditApplicationService) Append(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after map[string]any) {
	service.AppendWithMetadata(ctx, event, objectKey, recordID, principal, summary, before, after, nil)
}

func (service *AuditApplicationService) AppendWithMetadata(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
	if service == nil || service.AuditDomainService == nil {
		return
	}
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return
	}
	service.AuditDomainService.AppendWithMetadata(ctx, event, objectKey, recordID, principal, summary, before, after, metadata)
}

func (service *AuditApplicationService) Events(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return nil, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	events, err := service.AuditDomainService.Events(ctx, query, principal)
	if err != nil || service.projectEvents == nil {
		return events, err
	}
	return service.projectEvents(ctx, events, principal)
}

func (service *AuditApplicationService) SetEventProjector(projector func(context.Context, []auditmodel.AuditEvent, principalmodel.Principal) ([]auditmodel.AuditEvent, error)) {
	if service != nil {
		service.projectEvents = projector
	}
}

func (service *AuditApplicationService) Options(ctx context.Context, query auditmodel.AuditOptionQuery, principal principalmodel.Principal) ([]auditmodel.AuditOption, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return nil, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return service.AuditDomainService.Options(ctx, query, principal)
}

func AuditBuildEvent(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
	return (auditservice.AuditEventFactory{}).NewAuditEvent(ctx, auditcontract.AuditAppendRequest{
		Event: event, ObjectKey: objectKey, RecordID: recordID, Principal: principal,
		Summary: summary, Before: before, After: after, Metadata: metadata,
	})
}

func AuditRedactSensitiveMap(value map[string]any) map[string]any {
	return integrationpolicy.RedactSensitiveMap(value)
}

func AuditPrincipalWorkspaceID(principal principalmodel.Principal) string {
	if workspaceID := strings.TrimSpace(principal.WorkspaceID); workspaceID != "" {
		return workspaceID
	}
	return "default"
}
