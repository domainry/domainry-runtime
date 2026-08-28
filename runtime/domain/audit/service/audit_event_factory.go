package service

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	auditpolicy "github.com/domainry/domainry-runtime/runtime/domain/audit/policy"
)

// AuditEventFactory is the canonical Audit event constructor used through the
// contract.AuditEventFactory boundary.
type AuditEventFactory struct{}

func (AuditEventFactory) NewAuditEvent(ctx context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	return newAuditEvent(ctx, request)
}

func (s *AuditDomainService) NewAuditEvent(ctx context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	return AuditEventFactory{}.NewAuditEvent(ctx, request)
}

func newAuditEvent(ctx context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	principal := request.Principal
	metadata := auditpolicy.RedactSensitiveMap(request.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["workspace_id"] = principalWorkspaceID(principal)
	// A transport request ID identifies one delivery attempt, not the durable
	// business fact. Including it in a deterministically keyed audit event makes
	// a legitimate retry look like conflicting content in the repository.
	// Callers that need a stable business correlation may still provide it
	// explicitly in Metadata.
	if principal.RequestID != "" && strings.TrimSpace(request.IdempotencyKey) == "" {
		metadata["request_id"] = principal.RequestID
	}
	if principal.UserID != "" {
		metadata["actor_id"] = principal.UserID
	}
	if principal.RoleKey != "" {
		metadata["role_key"] = principal.RoleKey
	}
	now := time.Now().UTC()
	workspaceID := principalWorkspaceID(principal)
	eventID := auditmodel.NewEventID(now)
	if strings.TrimSpace(request.IdempotencyKey) != "" {
		eventID = auditmodel.IdempotentEventID(workspaceID, request.IdempotencyKey)
	}
	return auditmodel.AuditEvent{
		ID: eventID, WorkspaceID: workspaceID, Event: request.Event,
		ObjectKey: request.ObjectKey, RecordID: request.RecordID,
		ActorID: valueOrDefault(principal.UserID, "system"), RoleKey: principal.RoleKey,
		Summary: request.Summary, Metadata: metadata,
		Before: auditpolicy.RedactSensitiveMap(request.Before), After: auditpolicy.RedactSensitiveMap(request.After),
		CreatedAt: now.Format(time.RFC3339),
	}
}

func principalWorkspaceID(principal principalmodel.Principal) string {
	if workspaceID := strings.TrimSpace(principal.WorkspaceID); workspaceID != "" {
		return workspaceID
	}
	return "default"
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
