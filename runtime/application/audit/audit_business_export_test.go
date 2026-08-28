package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type businessAuditExportRepository struct {
	events   []auditmodel.AuditEvent
	appended []auditmodel.AuditEvent
}

func (r *businessAuditExportRepository) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	r.appended = append(r.appended, event)
	return nil
}
func (r *businessAuditExportRepository) ListAuditEvents(_ context.Context, _ string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	result := []auditmodel.AuditEvent{}
	for _, event := range r.events {
		if query.Event != "" && event.Event != query.Event || query.ObjectKey != "" && event.ObjectKey != query.ObjectKey || query.RecordID != "" && event.RecordID != query.RecordID || query.ActorID != "" && event.ActorID != query.ActorID || query.RoleKey != "" && event.RoleKey != query.RoleKey {
			continue
		}
		result = append(result, event)
	}
	return result, nil
}
func (*businessAuditExportRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}
func (*businessAuditExportRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, nil
}

type memoryBusinessAuditExportStore struct {
	artifact auditmodel.AuditBusinessExportArtifact
}

func (s *memoryBusinessAuditExportStore) CreateOrGetBusinessAuditExport(_ context.Context, requested auditmodel.AuditBusinessExportArtifact) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	if s.artifact.ID != "" {
		if s.artifact.ScopeSHA256 != requested.ScopeSHA256 || s.artifact.AuthorizationScopeSHA256 != requested.AuthorizationScopeSHA256 {
			return auditmodel.AuditBusinessExportArtifact{}, false, auditcontract.ErrBusinessAuditExportIdempotencyConflict
		}
		return s.artifact, false, nil
	}
	s.artifact = requested
	return requested, true, nil
}
func (s *memoryBusinessAuditExportStore) BusinessAuditExportByTokenHash(_ context.Context, workspaceID, tokenHash string) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	return s.artifact, s.artifact.WorkspaceID == workspaceID && s.artifact.TokenSHA256 == tokenHash, nil
}
func (s *memoryBusinessAuditExportStore) RecordBusinessAuditExportDownload(_ context.Context, workspaceID, id, downloadedAt string) (bool, error) {
	if s.artifact.WorkspaceID != workspaceID || s.artifact.ID != id {
		return false, errors.New("missing")
	}
	if s.artifact.DownloadCount > 0 {
		return false, nil
	}
	s.artifact.DownloadCount = 1
	s.artifact.LastDownloadedAt = downloadedAt
	return true, nil
}

func businessAuditExporterPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "auditor-a"}}, accessfixture.Bundle{
		Key: "business-auditor", RecordScope: "all_records", Permissions: []string{PermissionBusinessAuditRead, PermissionBusinessAuditExport},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "order", Scope: "all_records", Read: true}},
	})
}

func TestBusinessAuditExportPrepareDownloadHashIdempotencyAndSafety(t *testing.T) {
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	repository := &businessAuditExportRepository{events: []auditmodel.AuditEvent{{
		ID: "audit-1", Event: "order.completed", ObjectKey: "order", RecordID: "order-1", ActorID: "auditor-a", RoleKey: "business-auditor",
		Summary: "must not be exported", Metadata: map[string]any{"result": "completed", "token": "never-export"}, Before: map[string]any{"secret": "before"}, After: map[string]any{"secret": "after"}, CreatedAt: now.Format(time.RFC3339),
	}}}
	store := &memoryBusinessAuditExportStore{}
	service := NewAuditApplicationService(repository, store)
	service.ConfigureBusinessExport([]byte("0123456789abcdef0123456789abcdef"), func(context.Context, auditmodel.AuditBusinessExportFilter, principalmodel.Principal) error {
		return nil
	})
	service.SetBusinessExportClock(func() time.Time { return now })
	request := auditmodel.AuditBusinessExportRequest{Filters: auditmodel.AuditBusinessExportFilter{Event: "order.completed", Result: "completed"}, Format: "csv"}
	prepared, err := service.PrepareBusinessEventExport(t.Context(), request, "idem-1", businessAuditExporterPrincipal())
	if err != nil || prepared.RowCount != 1 || prepared.ContentSHA256 == "" || prepared.AuditIdentity == "" || prepared.DownloadToken == "" {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	if strings.Contains(store.artifact.TokenSHA256, prepared.DownloadToken) {
		t.Fatalf("token persisted in plaintext: %+v", store.artifact)
	}
	replayed, err := service.PrepareBusinessEventExport(t.Context(), request, "idem-1", businessAuditExporterPrincipal())
	if err != nil || replayed.DownloadToken != prepared.DownloadToken || replayed.ID != prepared.ID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	content, filename, err := service.DownloadBusinessEventExport(t.Context(), prepared.DownloadToken, businessAuditExporterPrincipal())
	if err != nil || filename != prepared.Filename || store.artifact.DownloadCount != 1 {
		t.Fatalf("download filename=%q count=%d err=%v", filename, store.artifact.DownloadCount, err)
	}
	if _, _, err := service.DownloadBusinessEventExport(t.Context(), prepared.DownloadToken, businessAuditExporterPrincipal()); err != nil || store.artifact.DownloadCount != 1 {
		t.Fatalf("idempotent replay count=%d err=%v", store.artifact.DownloadCount, err)
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != prepared.ContentSHA256 || !strings.Contains(string(content), "audit-1,order.completed,order,order-1,auditor-a,completed") {
		t.Fatalf("content hash/body mismatch: %q", content)
	}
	for _, forbidden := range []string{"never-export", "must not be exported", "before", "after"} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("CSV leaked %q: %q", forbidden, content)
		}
	}
	for _, event := range repository.appended {
		payload := fmt.Sprint(event.Metadata) + event.Summary
		if strings.Contains(payload, prepared.DownloadToken) {
			t.Fatalf("audit leaked token: %+v", event)
		}
	}
}

func TestBusinessAuditExportFailsClosedForPermissionScopeEmptyConflictAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	repository := &businessAuditExportRepository{}
	store := &memoryBusinessAuditExportStore{}
	service := NewAuditApplicationService(repository, store)
	service.ConfigureBusinessExport([]byte("0123456789abcdef0123456789abcdef"), func(_ context.Context, filters auditmodel.AuditBusinessExportFilter, _ principalmodel.Principal) error {
		if filters.RecordID == "denied" {
			return apperror.New(apperror.KindNotFound, "backend.record.not_found", nil, nil)
		}
		return nil
	})
	service.SetBusinessExportClock(func() time.Time { return now })
	principal := businessAuditExporterPrincipal()
	withoutExport := principal
	withoutExport = accessfixture.WithMutation(withoutExport, func(role *accessfixture.Bundle) {
		role.Permissions = []string{PermissionBusinessAuditRead}
	})
	if _, err := service.PrepareBusinessEventExport(t.Context(), auditmodel.AuditBusinessExportRequest{}, "idem", withoutExport); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("permission error=%v", err)
	}
	if _, err := service.PrepareBusinessEventExport(t.Context(), auditmodel.AuditBusinessExportRequest{Filters: auditmodel.AuditBusinessExportFilter{ActorID: "other"}}, "idem", principal); apperror.CodeOf(err) != "backend.audit.export_actor_scope_denied" {
		t.Fatalf("actor error=%v", err)
	}
	if _, err := service.PrepareBusinessEventExport(t.Context(), auditmodel.AuditBusinessExportRequest{Filters: auditmodel.AuditBusinessExportFilter{ObjectKey: "order", RecordID: "denied"}}, "idem", principal); apperror.CodeOf(err) != "backend.record.not_found" {
		t.Fatalf("scope error=%v", err)
	}
	if _, err := service.PrepareBusinessEventExport(t.Context(), auditmodel.AuditBusinessExportRequest{}, "idem", principal); apperror.CodeOf(err) != "backend.audit.export_no_results" {
		t.Fatalf("empty error=%v", err)
	}
	repository.events = []auditmodel.AuditEvent{{ID: "audit-1", Event: "order.completed", ActorID: principal.UserID, CreatedAt: now.Format(time.RFC3339)}}
	prepared, err := service.PrepareBusinessEventExport(t.Context(), auditmodel.AuditBusinessExportRequest{}, "idem", principal)
	if err != nil {
		t.Fatal(err)
	}
	changed := principal
	changed = accessfixture.WithMutation(changed, func(role *accessfixture.Bundle) {
		role.DataPolicies = nil
	})
	if _, _, err := service.DownloadBusinessEventExport(t.Context(), prepared.DownloadToken, changed); apperror.CodeOf(err) != "backend.audit.export_scope_changed" {
		t.Fatalf("changed scope error=%v", err)
	}
	service.SetBusinessExportClock(func() time.Time { return now.Add(businessAuditExportTTL + time.Second) })
	if _, _, err := service.DownloadBusinessEventExport(t.Context(), prepared.DownloadToken, principal); apperror.CodeOf(err) != "backend.audit.export_download_expired" {
		t.Fatalf("expiry error=%v", err)
	}
}
