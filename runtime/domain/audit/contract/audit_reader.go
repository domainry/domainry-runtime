package contract

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// AuditReader exposes stable audit read capabilities without leaking AuditRepository.
type AuditReader interface {
	ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
	ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
	ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error)
}
