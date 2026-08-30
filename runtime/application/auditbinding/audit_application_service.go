package auditbinding

import (
	"context"

	auditapplication "github.com/domainry/domainry-audit-sdk/application"
	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AuditApplicationService = auditapplication.Service[principalmodel.Principal, principalmodel.SystemScope]
type AuditAppendRequest = auditapplication.AppendRequest[principalmodel.Principal]
type AuditAppender = auditapplication.Appender[principalmodel.Principal]
type AuditTelemetryAppender = auditapplication.TelemetryAppender[principalmodel.Principal]
type AuditEventFactory = auditapplication.EventFactory[principalmodel.Principal]
type AuditReader = auditapplication.Reader[principalmodel.SystemScope]
type AuditRepository = auditapplication.Store[principalmodel.SystemScope]
type AuditEventWriterRepository = auditapplication.EventWriterStore
type AuditEventRepository = auditapplication.EventStore

type BusinessAuditEventDTO = auditapplication.BusinessAuditEventDTO
type TenantGovernanceAuditEventDTO = auditapplication.TenantGovernanceAuditEventDTO
type OperationsAuditEventDTO = auditapplication.OperationsAuditEventDTO
type SurfaceAuditResult[T any] = auditapplication.SurfaceAuditResult[T]
type BusinessAuditExportPrepared = auditapplication.BusinessAuditExportPrepared

const (
	PermissionBusinessAuditRead      = auditapplication.PermissionBusinessAuditRead
	PermissionBusinessAuditExport    = auditapplication.PermissionBusinessAuditExport
	PermissionTenantGovernanceRead   = auditapplication.PermissionTenantGovernanceRead
	PermissionTenantGovernanceExport = auditapplication.PermissionTenantGovernanceExport
	PermissionOperationsAuditRead    = auditapplication.PermissionOperationsAuditRead
	PermissionOperationsAuditExport  = auditapplication.PermissionOperationsAuditExport
)

func NewAuditApplicationService(store AuditRepository, exporters ...auditcontract.Exporter) *AuditApplicationService {
	return auditapplication.NewService(store, runtimePolicy(), exporters...)
}

func runtimePolicy() auditapplication.Policy[principalmodel.Principal, principalmodel.SystemScope] {
	return auditapplication.Policy[principalmodel.Principal, principalmodel.SystemScope]{
		Actor: func(principal principalmodel.Principal) auditcontract.Actor {
			kind := "user"
			if !principal.Known || principal.SystemScope.Valid() {
				kind = "system"
			}
			return auditcontract.Actor{WorkspaceID: principal.WorkspaceID, SubjectID: principal.UserID, RoleKey: principal.RoleKey, Kind: kind, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, AuthorizationRevision: principal.AuthorizationRevision}
		},
		WorkspaceID: func(principal principalmodel.Principal) string { return principal.WorkspaceID },
		ValidateCommand: func(principal principalmodel.Principal) error {
			_, err := principalmodel.CommandScopeForPrincipal(principal)
			return err
		},
		ValidateQuery: func(principal principalmodel.Principal) error {
			_, err := principalmodel.QueryScopeForPrincipal(principal)
			return err
		},
		ValidateSystemQuery: func(scope principalmodel.SystemScope) error {
			_, err := principalmodel.NewSystemQueryScope(scope)
			return err
		},
		Known: func(principal principalmodel.Principal) bool { return principal.Known },
		CanView: func(principal principalmodel.Principal) bool {
			return principal.HasPermission("identity.audit.view") || principal.Allows("identity_permission", "read")
		},
		HasPermission: func(principal principalmodel.Principal, permission string, inherited bool) bool {
			if inherited {
				return principal.HasPermission(permission)
			}
			return principal.HasExactPermission(permission)
		},
		ExportPrincipal: func(principal principalmodel.Principal) auditcontract.ExportPrincipal {
			return auditcontract.ExportPrincipal{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, AuthorizationRevision: principal.AuthorizationRevision, SystemScope: string(principal.SystemScope.Kind), SystemCapabilities: append([]string(nil), principal.SystemCapabilities...), AuthorizationContext: principal}
		},
	}
}

func AuditBuildEvent(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) auditcontract.AuditEvent {
	return auditapplication.NewService[principalmodel.Principal, principalmodel.SystemScope](nil, runtimePolicy()).NewAuditEvent(ctx, AuditAppendRequest{Event: event, ObjectKey: objectKey, RecordID: recordID, Principal: principal, Summary: summary, Before: before, After: after, Metadata: metadata})
}
func AuditRedactSensitiveMap(value map[string]any) map[string]any {
	return auditapplication.RedactSensitiveMap(value)
}
func AuditPrincipalWorkspaceID(principal principalmodel.Principal) string {
	return principal.WorkspaceID
}
