package auditbinding

import (
	"context"

	auditapplication "github.com/domainry/domainry-audit-sdk/application"
	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// auditBusinessReadAction is owned by module:audit. Runtime references the
// source-owned Action key when its embedded Audit application service reads
// business events; it does not define an alias or a Runtime permission.
const auditBusinessReadAction = "audit.business.read"

type AuditApplicationService = auditapplication.Service[principalmodel.Principal, principalmodel.SystemScope]
type AuditAppendRequest = auditapplication.AppendRequest[principalmodel.Principal]
type AuditAppender = auditapplication.Appender[principalmodel.Principal]
type AuditTelemetryAppender = auditapplication.TelemetryAppender[principalmodel.Principal]
type AuditEventFactory = auditapplication.EventFactory[principalmodel.Principal]
type AuditReader = auditapplication.Reader[principalmodel.SystemScope]
type AuditRepository = auditapplication.Store[principalmodel.SystemScope]
type AuditEventWriterRepository = auditapplication.EventWriterStore
type AuditEventRepository = auditapplication.EventStore

func NewAuditApplicationService(store AuditRepository) *AuditApplicationService {
	return auditapplication.NewService(store, runtimePolicy())
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
			return principal.HasExactPermission(auditBusinessReadAction)
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
