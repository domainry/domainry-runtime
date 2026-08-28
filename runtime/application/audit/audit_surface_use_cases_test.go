package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type surfaceAuditRepository struct {
	events []auditmodel.AuditEvent
	query  auditmodel.AuditEventQuery
	err    error
}

func (*surfaceAuditRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	return nil
}
func (r *surfaceAuditRepository) ListAuditEvents(_ context.Context, _ string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	r.query = query
	return append([]auditmodel.AuditEvent(nil), r.events...), r.err
}
func (r *surfaceAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return append([]auditmodel.AuditEvent(nil), r.events...), nil
}
func (*surfaceAuditRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, nil
}

func TestAuditSurfaceUseCasesFilterProjectAndClampRetention(t *testing.T) {
	repository := &surfaceAuditRepository{events: []auditmodel.AuditEvent{
		{ID: "business", Event: "order_updated", ObjectKey: "order", ActorID: "user-a", Before: map[string]any{"password": "secret", "status": "new"}},
		{ID: "governance", Event: "identity_role_updated", ObjectKey: "role", Metadata: map[string]any{"token": "secret"}},
		{ID: "ops", Event: "scheduler_dead_letter_resolved", ObjectKey: "job_dead_letter", Before: map[string]any{"customer": "private"}, Metadata: map[string]any{"password": "secret"}},
	}}
	service := NewAuditApplicationService(repository)

	businessPrincipal := auditSurfacePrincipal(PermissionBusinessAuditRead)
	business, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{}, businessPrincipal)
	if err != nil || len(business.Items) != 1 || business.Items[0].ID != "business" {
		t.Fatalf("business=%+v err=%v", business, err)
	}
	if repository.query.ActorID != businessPrincipal.UserID || repository.query.CreatedFrom == "" || repository.query.Limit != 201 || repository.query.Class != auditmodel.AuditEventClassBusiness || business.PageSize != 200 {
		t.Fatalf("business query was not server constrained: %+v", repository.query)
	}
	payload, _ := json.Marshal(business)
	if strings.Contains(string(payload), "secret") || business.RetentionClass != "business_history" {
		t.Fatalf("business projection=%s", payload)
	}

	governance, err := service.TenantGovernanceEvents(t.Context(), auditmodel.AuditEventQuery{}, auditSurfacePrincipal(PermissionTenantGovernanceRead))
	if err != nil || len(governance.Items) != 1 || governance.Items[0].ID != "governance" {
		t.Fatalf("governance=%+v err=%v", governance, err)
	}
	if repository.query.Class != auditmodel.AuditEventClassGovernance {
		t.Fatalf("governance query class=%q", repository.query.Class)
	}
	payload, _ = json.Marshal(governance)
	if strings.Contains(string(payload), "secret") || governance.RetentionDays != governanceAuditRetentionDays {
		t.Fatalf("governance projection=%s", payload)
	}

	ops, err := service.OperationsEvents(t.Context(), auditmodel.AuditEventQuery{}, auditSurfacePrincipal(PermissionOperationsAuditRead))
	if err != nil || len(ops.Items) != 1 || ops.Items[0].ID != "ops" {
		t.Fatalf("ops=%+v err=%v", ops, err)
	}
	if repository.query.Class != auditmodel.AuditEventClassOperations {
		t.Fatalf("operations query class=%q", repository.query.Class)
	}
	payload, _ = json.Marshal(ops)
	if strings.Contains(string(payload), "before") || strings.Contains(string(payload), "customer") || strings.Contains(string(payload), "secret") {
		t.Fatalf("Ops projection leaked business data: %s", payload)
	}
}

func TestAuditSurfaceExportRequiresSeparatePermission(t *testing.T) {
	service := NewAuditApplicationService(&surfaceAuditRepository{})
	reader := auditSurfacePrincipal(PermissionTenantGovernanceRead)
	if _, err := service.TenantGovernanceExport(t.Context(), auditmodel.AuditEventQuery{}, reader); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("read permission unexpectedly granted export: %v", err)
	}
	exporter := auditSurfacePrincipal(PermissionTenantGovernanceRead, PermissionTenantGovernanceExport)
	if _, err := service.TenantGovernanceExport(t.Context(), auditmodel.AuditEventQuery{}, exporter); err != nil {
		t.Fatalf("governance export rejected: %v", err)
	}
}

func TestAuditSurfacePermissionScopeProjectionAndRetentionEdges(t *testing.T) {
	service := NewAuditApplicationService(&surfaceAuditRepository{})
	withoutPermissions := auditSurfacePrincipal()
	for name, call := range map[string]func() error{
		"business": func() error {
			_, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{}, withoutPermissions)
			return err
		},
		"governance": func() error {
			_, err := service.TenantGovernanceEvents(t.Context(), auditmodel.AuditEventQuery{}, withoutPermissions)
			return err
		},
		"operations": func() error {
			_, err := service.OperationsEvents(t.Context(), auditmodel.AuditEventQuery{}, withoutPermissions)
			return err
		},
		"operations export": func() error {
			_, err := service.OperationsExport(t.Context(), auditmodel.AuditEventQuery{}, withoutPermissions)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("permission error=%v", err)
			}
		})
	}

	unknown := auditSurfacePrincipal(PermissionBusinessAuditRead)
	unknown.Known = false
	if _, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{}, unknown); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("unknown principal error=%v", err)
	}

	missingWorkspace := auditSurfacePrincipal(PermissionBusinessAuditRead)
	missingWorkspace.WorkspaceID = ""
	if _, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{}, missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}

	repository := &surfaceAuditRepository{events: []auditmodel.AuditEvent{{
		ID: "business", Event: "order_updated", ObjectKey: "order", RecordID: "order-1",
	}}}
	service = NewAuditApplicationService(repository)
	projectCalls := 0
	service.SetEventProjector(func(_ context.Context, events []auditmodel.AuditEvent, _ principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
		projectCalls++
		return events, nil
	})
	oldest := time.Now().UTC().AddDate(0, 0, -(businessAuditRetentionDays + 1)).Format(time.RFC3339)
	result, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{
		ObjectKey: "order", RecordID: " ", CreatedFrom: oldest, Limit: 1001,
	}, auditSurfacePrincipal(PermissionBusinessAuditRead))
	if err != nil || result.Count != 1 || projectCalls != 1 || repository.query.Limit != 201 || result.PageSize != 200 || repository.query.CreatedFrom == oldest {
		t.Fatalf("result=%+v query=%+v project_calls=%d error=%v", result, repository.query, projectCalls, err)
	}
	recent := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{
		ObjectKey: "order", RecordID: "order-1", CreatedFrom: recent, Limit: 10,
	}, auditSurfacePrincipal(PermissionBusinessAuditRead)); err != nil ||
		repository.query.RecordID != "order-1" || repository.query.CreatedFrom != recent || repository.query.Limit != 11 {
		t.Fatalf("bounded query=%+v error=%v", repository.query, err)
	}
	if _, err := service.BusinessEvents(t.Context(), auditmodel.AuditEventQuery{Cursor: "invalid"}, auditSurfacePrincipal(PermissionBusinessAuditRead)); apperror.CodeOf(err) != "backend.audit.cursor_invalid" || apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("invalid cursor error=%v", err)
	}

	repository.err = apperror.New(apperror.KindInternal, "backend.audit.repository_failed", nil, nil)
	if _, err := service.TenantGovernanceEvents(t.Context(), auditmodel.AuditEventQuery{}, auditSurfacePrincipal(PermissionTenantGovernanceRead)); err == nil {
		t.Fatal("governance repository error was ignored")
	}
	if _, err := service.OperationsEvents(t.Context(), auditmodel.AuditEventQuery{}, auditSurfacePrincipal(PermissionOperationsAuditRead)); err == nil {
		t.Fatal("operations repository error was ignored")
	}
	repository.err = nil
	if _, err := service.OperationsExport(t.Context(), auditmodel.AuditEventQuery{}, auditSurfacePrincipal(
		PermissionOperationsAuditRead,
		PermissionOperationsAuditExport,
	)); err != nil {
		t.Fatalf("operations export error=%v", err)
	}
}

func TestBusinessAuditSurfaceReturnsOpaqueContinuation(t *testing.T) {
	repository := &surfaceAuditRepository{events: []auditmodel.AuditEvent{
		{ID: "audit-3", Event: "order.updated", ObjectKey: "order", ActorID: "user-a", CreatedAt: "2026-08-18T12:00:00Z"},
		{ID: "audit-2", Event: "order.updated", ObjectKey: "order", ActorID: "user-a", CreatedAt: "2026-08-18T12:00:00Z"},
		{ID: "audit-1", Event: "order.updated", ObjectKey: "order", ActorID: "user-a", CreatedAt: "2026-08-18T11:59:59Z"},
	}}
	result, err := NewAuditApplicationService(repository).BusinessEvents(t.Context(), auditmodel.AuditEventQuery{Limit: 2}, auditSurfacePrincipal(PermissionBusinessAuditRead))
	if err != nil || result.Count != 2 || result.PageSize != 2 || !result.Truncated || result.NextCursor == "" || repository.query.Limit != 3 {
		t.Fatalf("result=%+v query=%+v err=%v", result, repository.query, err)
	}
	cursor, err := auditmodel.DecodeAuditEventCursor(result.NextCursor)
	if err != nil || cursor.ID != "audit-2" || cursor.CreatedAt != "2026-08-18T12:00:00Z" {
		t.Fatalf("cursor=%+v err=%v", cursor, err)
	}
}

func auditSurfacePrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Permissions: permissions, RecordScope: "all_records"})
}
