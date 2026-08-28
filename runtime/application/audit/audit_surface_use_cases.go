package audit

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	PermissionBusinessAuditRead      = "audit.business.read"
	PermissionTenantGovernanceRead   = "audit.governance.read"
	PermissionTenantGovernanceExport = "audit.governance.export"
	PermissionOperationsAuditRead    = "audit.ops.read"
	PermissionOperationsAuditExport  = "audit.ops.export"

	businessAuditRetentionDays   = 365
	governanceAuditRetentionDays = 2555
	operationsAuditRetentionDays = 90
	businessAuditMaxPageSize     = 200
)

type BusinessAuditEventDTO struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	ObjectKey string         `json:"object_key,omitempty"`
	RecordID  string         `json:"record_id,omitempty"`
	ActorID   string         `json:"actor_id"`
	Summary   string         `json:"summary"`
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type TenantGovernanceAuditEventDTO struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	ObjectKey string         `json:"object_key,omitempty"`
	RecordID  string         `json:"record_id,omitempty"`
	ActorID   string         `json:"actor_id"`
	RoleKey   string         `json:"role_key,omitempty"`
	Summary   string         `json:"summary"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type OperationsAuditEventDTO struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	ObjectKey string         `json:"object_key,omitempty"`
	RecordID  string         `json:"record_id,omitempty"`
	ActorID   string         `json:"actor_id"`
	Summary   string         `json:"summary"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type SurfaceAuditResult[T any] struct {
	Items          []T    `json:"items"`
	Count          int    `json:"count"`
	PageSize       int    `json:"page_size"`
	Truncated      bool   `json:"truncated"`
	NextCursor     string `json:"next_cursor,omitempty"`
	RetentionClass string `json:"retention_class"`
	RetentionDays  int    `json:"retention_days"`
}

func (service *AuditApplicationService) BusinessEvents(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) (SurfaceAuditResult[BusinessAuditEventDTO], error) {
	if err := requireAuditPermission(principal, PermissionBusinessAuditRead, false); err != nil {
		return SurfaceAuditResult[BusinessAuditEventDTO]{}, err
	}
	if strings.TrimSpace(query.ObjectKey) == "" || strings.TrimSpace(query.RecordID) == "" {
		query.ActorID = principal.UserID
	}
	query = applyAuditRetention(query, businessAuditRetentionDays)
	if query.Limit > businessAuditMaxPageSize {
		query.Limit = businessAuditMaxPageSize
	}
	if query.Cursor != "" {
		if _, err := auditmodel.DecodeAuditEventCursor(query.Cursor); err != nil {
			return SurfaceAuditResult[BusinessAuditEventDTO]{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.audit.cursor_invalid", Err: err}
		}
	}
	pageSize := query.Limit
	query.Limit = pageSize + 1
	query.Class = auditmodel.AuditEventClassBusiness
	events, err := service.surfaceEvents(ctx, query, principal)
	if err != nil {
		return SurfaceAuditResult[BusinessAuditEventDTO]{}, err
	}
	truncated := len(events) > pageSize
	if truncated {
		events = events[:pageSize]
	}
	nextCursor := ""
	if truncated && len(events) > 0 {
		nextCursor = auditmodel.EncodeAuditEventCursor(events[len(events)-1])
	}
	items := []BusinessAuditEventDTO{}
	for _, event := range events {
		if auditmodel.ClassifyAuditEvent(event) != auditmodel.AuditEventClassBusiness {
			continue
		}
		items = append(items, BusinessAuditEventDTO{
			ID: event.ID, Event: event.Event, ObjectKey: event.ObjectKey, RecordID: event.RecordID,
			ActorID: event.ActorID, Summary: event.Summary, Before: AuditRedactSensitiveMap(event.Before),
			After: AuditRedactSensitiveMap(event.After), CreatedAt: event.CreatedAt,
		})
	}
	return SurfaceAuditResult[BusinessAuditEventDTO]{Items: items, Count: len(items), PageSize: pageSize, Truncated: truncated, NextCursor: nextCursor, RetentionClass: "business_history", RetentionDays: businessAuditRetentionDays}, nil
}

func (service *AuditApplicationService) TenantGovernanceEvents(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) (SurfaceAuditResult[TenantGovernanceAuditEventDTO], error) {
	if err := requireAuditPermission(principal, PermissionTenantGovernanceRead, true); err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	query = applyAuditRetention(query, governanceAuditRetentionDays)
	query.Class = auditmodel.AuditEventClassGovernance
	events, err := service.surfaceEvents(ctx, query, principal)
	if err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	items := []TenantGovernanceAuditEventDTO{}
	for _, event := range events {
		if auditmodel.ClassifyAuditEvent(event) != auditmodel.AuditEventClassGovernance {
			continue
		}
		items = append(items, TenantGovernanceAuditEventDTO{
			ID: event.ID, Event: event.Event, ObjectKey: event.ObjectKey, RecordID: event.RecordID,
			ActorID: event.ActorID, RoleKey: event.RoleKey, Summary: event.Summary,
			Metadata: AuditRedactSensitiveMap(event.Metadata), Before: AuditRedactSensitiveMap(event.Before),
			After: AuditRedactSensitiveMap(event.After), CreatedAt: event.CreatedAt,
		})
	}
	return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{Items: items, Count: len(items), PageSize: query.Limit, RetentionClass: "tenant_governance", RetentionDays: governanceAuditRetentionDays}, nil
}

func (service *AuditApplicationService) OperationsEvents(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) (SurfaceAuditResult[OperationsAuditEventDTO], error) {
	if err := requireAuditPermission(principal, PermissionOperationsAuditRead, false); err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	query = applyAuditRetention(query, operationsAuditRetentionDays)
	query.Class = auditmodel.AuditEventClassOperations
	events, err := service.surfaceEvents(ctx, query, principal)
	if err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	items := []OperationsAuditEventDTO{}
	for _, event := range events {
		if auditmodel.ClassifyAuditEvent(event) != auditmodel.AuditEventClassOperations {
			continue
		}
		items = append(items, OperationsAuditEventDTO{
			ID: event.ID, Event: event.Event, ObjectKey: event.ObjectKey, RecordID: event.RecordID,
			ActorID: event.ActorID, Summary: event.Summary, Metadata: AuditRedactSensitiveMap(event.Metadata), CreatedAt: event.CreatedAt,
		})
	}
	return SurfaceAuditResult[OperationsAuditEventDTO]{Items: items, Count: len(items), PageSize: query.Limit, RetentionClass: "technical_security", RetentionDays: operationsAuditRetentionDays}, nil
}

func (service *AuditApplicationService) TenantGovernanceExport(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) (SurfaceAuditResult[TenantGovernanceAuditEventDTO], error) {
	if err := requireAuditPermission(principal, PermissionTenantGovernanceExport, true); err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	return service.TenantGovernanceEvents(ctx, query, principal)
}

func (service *AuditApplicationService) OperationsExport(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) (SurfaceAuditResult[OperationsAuditEventDTO], error) {
	if err := requireAuditPermission(principal, PermissionOperationsAuditExport, false); err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	return service.OperationsEvents(ctx, query, principal)
}

func (service *AuditApplicationService) surfaceEvents(ctx context.Context, query auditmodel.AuditEventQuery, principal principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return nil, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	events, err := service.ListAuditEvents(ctx, AuditPrincipalWorkspaceID(principal), query)
	if err != nil || service.projectEvents == nil {
		return events, err
	}
	return service.projectEvents(ctx, events, principal)
}

func requireAuditPermission(principal principalmodel.Principal, permission string, allowWorkspaceAdmin bool) error {
	allowed := principal.HasExactPermission(permission)
	if allowWorkspaceAdmin {
		allowed = principal.HasPermission(permission)
	}
	if !principal.Known || !allowed {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.audit.view_permission_required"}
	}
	return nil
}

func applyAuditRetention(query auditmodel.AuditEventQuery, days int) auditmodel.AuditEventQuery {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if current, err := time.Parse(time.RFC3339, strings.TrimSpace(query.CreatedFrom)); err != nil || current.Before(cutoff) {
		query.CreatedFrom = cutoff.Format(time.RFC3339)
	}
	if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 200
	}
	return query
}
