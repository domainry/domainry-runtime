package repository

import (
	"context"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// AuditRepository owns audit persistence operations used by the domain layer.
type AuditRepository interface {
	InsertAuditEvent(ctx context.Context, workspaceID string, event auditmodel.AuditEvent) error
	ListAuditEvents(ctx context.Context, workspaceID string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
	ListAuditEventsForSystem(ctx context.Context, scope principalmodel.SystemScope, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
	ListAuditOptions(ctx context.Context, workspaceID string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error)
}

// AuditEventWriterRepository is the minimal persistence contract for use cases
// that only append mandatory audit evidence.
type AuditEventWriterRepository interface {
	InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error
}

// AuditEventRepository persists and reads audit events without exposing
// auxiliary option queries to callers that do not need them.
type AuditEventRepository interface {
	AuditEventWriterRepository
	ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
}
