package audit

import (
	"context"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	PermissionBusinessAuditRead      = "audit.business.read"
	PermissionTenantGovernanceRead   = "audit.governance.read"
	PermissionTenantGovernanceExport = "audit.governance.export"
	PermissionOperationsAuditRead    = "audit.ops.read"
	PermissionOperationsAuditExport  = "audit.ops.export"
)
const (
	businessAuditRetentionDays   = auditmodel.BusinessRetentionDays
	governanceAuditRetentionDays = auditmodel.GovernanceRetentionDays
	operationsAuditRetentionDays = auditmodel.OperationsRetentionDays
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

func (s *AuditApplicationService) BusinessEvents(ctx context.Context, q auditmodel.AuditEventQuery, p principalmodel.Principal) (SurfaceAuditResult[BusinessAuditEventDTO], error) {
	if err := requireAuditPermission(p, PermissionBusinessAuditRead, false); err != nil {
		return SurfaceAuditResult[BusinessAuditEventDTO]{}, err
	}
	r, err := s.sharedSurface(ctx, auditmodel.SurfaceBusiness, q, p)
	if err != nil {
		return SurfaceAuditResult[BusinessAuditEventDTO]{}, err
	}
	items := make([]BusinessAuditEventDTO, 0, len(r.Items))
	for _, e := range r.Items {
		items = append(items, BusinessAuditEventDTO{ID: e.ID, Event: e.Event, ObjectKey: e.ObjectKey, RecordID: e.RecordID, ActorID: e.ActorID, Summary: e.Summary, Before: e.Before, After: e.After, CreatedAt: e.CreatedAt})
	}
	return surfaceResult(r, items), nil
}
func (s *AuditApplicationService) TenantGovernanceEvents(ctx context.Context, q auditmodel.AuditEventQuery, p principalmodel.Principal) (SurfaceAuditResult[TenantGovernanceAuditEventDTO], error) {
	if err := requireAuditPermission(p, PermissionTenantGovernanceRead, true); err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	r, err := s.sharedSurface(ctx, auditmodel.SurfaceGovernance, q, p)
	if err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	items := make([]TenantGovernanceAuditEventDTO, 0, len(r.Items))
	for _, e := range r.Items {
		items = append(items, TenantGovernanceAuditEventDTO{ID: e.ID, Event: e.Event, ObjectKey: e.ObjectKey, RecordID: e.RecordID, ActorID: e.ActorID, RoleKey: e.RoleKey, Summary: e.Summary, Metadata: e.Metadata, Before: e.Before, After: e.After, CreatedAt: e.CreatedAt})
	}
	return surfaceResult(r, items), nil
}
func (s *AuditApplicationService) OperationsEvents(ctx context.Context, q auditmodel.AuditEventQuery, p principalmodel.Principal) (SurfaceAuditResult[OperationsAuditEventDTO], error) {
	if err := requireAuditPermission(p, PermissionOperationsAuditRead, false); err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	r, err := s.sharedSurface(ctx, auditmodel.SurfaceOperations, q, p)
	if err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	items := make([]OperationsAuditEventDTO, 0, len(r.Items))
	for _, e := range r.Items {
		items = append(items, OperationsAuditEventDTO{ID: e.ID, Event: e.Event, ObjectKey: e.ObjectKey, RecordID: e.RecordID, ActorID: e.ActorID, Summary: e.Summary, Metadata: e.Metadata, CreatedAt: e.CreatedAt})
	}
	return surfaceResult(r, items), nil
}
func (s *AuditApplicationService) TenantGovernanceExport(ctx context.Context, q auditmodel.AuditEventQuery, p principalmodel.Principal) (SurfaceAuditResult[TenantGovernanceAuditEventDTO], error) {
	if err := requireAuditPermission(p, PermissionTenantGovernanceExport, true); err != nil {
		return SurfaceAuditResult[TenantGovernanceAuditEventDTO]{}, err
	}
	return s.TenantGovernanceEvents(ctx, q, p)
}
func (s *AuditApplicationService) OperationsExport(ctx context.Context, q auditmodel.AuditEventQuery, p principalmodel.Principal) (SurfaceAuditResult[OperationsAuditEventDTO], error) {
	if err := requireAuditPermission(p, PermissionOperationsAuditExport, false); err != nil {
		return SurfaceAuditResult[OperationsAuditEventDTO]{}, err
	}
	return s.OperationsEvents(ctx, q, p)
}
func (s *AuditApplicationService) sharedSurface(ctx context.Context, kind auditmodel.SurfaceKind, q auditmodel.AuditEventQuery, p principalmodel.Principal) (auditmodel.SurfaceResult, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(p); err != nil {
		return auditmodel.SurfaceResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	plan, err := auditmodel.PlanSurface(kind, q, p.UserID, time.Now())
	if err != nil {
		return auditmodel.SurfaceResult{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.audit.cursor_invalid", Err: err}
	}
	events, err := s.ListAuditEvents(ctx, AuditPrincipalWorkspaceID(p), plan.Query)
	if err != nil {
		return auditmodel.SurfaceResult{}, err
	}
	if s.projectEvents != nil {
		events, err = s.projectEvents(ctx, events, p)
		if err != nil {
			return auditmodel.SurfaceResult{}, err
		}
	}
	return auditmodel.ProjectSurface(events, plan), nil
}
func requireAuditPermission(p principalmodel.Principal, permission string, allowWorkspaceAdmin bool) error {
	allowed := p.HasExactPermission(permission)
	if allowWorkspaceAdmin {
		allowed = p.HasPermission(permission)
	}
	if !p.Known || !allowed {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.audit.view_permission_required"}
	}
	return nil
}
func surfaceResult[T any](r auditmodel.SurfaceResult, items []T) SurfaceAuditResult[T] {
	return SurfaceAuditResult[T]{Items: items, Count: len(items), PageSize: r.PageSize, Truncated: r.Truncated, NextCursor: r.NextCursor, RetentionClass: r.RetentionClass, RetentionDays: r.RetentionDays}
}
