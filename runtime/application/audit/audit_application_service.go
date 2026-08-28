package audit

import (
	"context"
	"strings"
	"time"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	auditservice "github.com/domainry/domainry-runtime/runtime/domain/audit/service"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type AuditApplicationService struct {
	*auditservice.AuditDomainService
	projectEvents        func(context.Context, []auditmodel.AuditEvent, principalmodel.Principal) ([]auditmodel.AuditEvent, error)
	exportStore          auditcontract.AuditBusinessExportStore
	exportTokenKey       []byte
	authorizeExportScope func(context.Context, auditmodel.AuditBusinessExportFilter, principalmodel.Principal) error
	clock                func() time.Time
}

func NewAuditApplicationService(repository auditrepository.AuditRepository, exportStores ...auditcontract.AuditBusinessExportStore) *AuditApplicationService {
	service := &AuditApplicationService{AuditDomainService: auditservice.NewAuditDomainService(repository), clock: time.Now}
	if len(exportStores) > 0 {
		service.exportStore = exportStores[0]
	}
	return service
}

func (service *AuditApplicationService) SetBusinessExportClock(clock func() time.Time) {
	if service != nil && clock != nil {
		service.clock = clock
	}
}

func (service *AuditApplicationService) ConfigureBusinessExport(tokenKey []byte, authorizer func(context.Context, auditmodel.AuditBusinessExportFilter, principalmodel.Principal) error) {
	if service != nil {
		service.exportTokenKey = append([]byte(nil), tokenKey...)
		service.authorizeExportScope = authorizer
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
