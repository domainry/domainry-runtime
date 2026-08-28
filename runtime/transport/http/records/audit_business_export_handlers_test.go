package records

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type recordsBusinessAuditExportStore struct {
	artifact auditmodel.AuditBusinessExportArtifact
}

func (s *recordsBusinessAuditExportStore) CreateOrGetBusinessAuditExport(_ context.Context, value auditmodel.AuditBusinessExportArtifact) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	if s.artifact.ID != "" {
		return s.artifact, false, nil
	}
	s.artifact = value
	return value, true, nil
}

func (s *recordsBusinessAuditExportStore) BusinessAuditExportByTokenHash(_ context.Context, workspaceID, tokenHash string) (auditmodel.AuditBusinessExportArtifact, bool, error) {
	return s.artifact, s.artifact.WorkspaceID == workspaceID && s.artifact.TokenSHA256 == tokenHash, nil
}

func (s *recordsBusinessAuditExportStore) RecordBusinessAuditExportDownload(_ context.Context, workspaceID, artifactID, downloadedAt string) (bool, error) {
	if s.artifact.WorkspaceID != workspaceID || s.artifact.ID != artifactID {
		return false, nil
	}
	if s.artifact.DownloadCount > 0 {
		return false, nil
	}
	s.artifact.DownloadCount = 1
	s.artifact.LastDownloadedAt = downloadedAt
	return true, nil
}

func TestBusinessAuditExportHandlersRejectUnknownFiltersAndReturnServerBytes(t *testing.T) {
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "auditor"}}, accessfixture.Bundle{
		Key: "business-auditor", RecordScope: "all_records", Permissions: []string{auditapplication.PermissionBusinessAuditRead, auditapplication.PermissionBusinessAuditExport},
	})
	repository := &recordsAuditRepository{events: []auditmodel.AuditEvent{{
		ID: "audit-1", Event: "order.completed", ObjectKey: "order", RecordID: "order-1", ActorID: "auditor", Metadata: map[string]any{"result": "completed", "secret": "hidden"}, CreatedAt: now.Format(time.RFC3339),
	}}}
	store := &recordsBusinessAuditExportStore{}
	service := auditapplication.NewAuditApplicationService(repository, store)
	service.ConfigureBusinessExport([]byte("0123456789abcdef0123456789abcdef"), nil)
	service.SetBusinessExportClock(func() time.Time { return now })
	handler, serviceErr := recordsHandlerForTest(principal)
	handler.audit = service

	invalid := recordsRequest(http.MethodPost, "/business/audit-event-exports", `{"filters":{"event":"order.completed","raw_payload":true}}`, nil)
	invalid.Header.Set("Idempotency-Key", "invalid")
	handler.prepareBusinessAuditEventExport(httptest.NewRecorder(), invalid)
	if apperror.CodeOf(*serviceErr) != "backend.audit.export_request_invalid" {
		t.Fatalf("unknown filter error=%v", *serviceErr)
	}

	*serviceErr = nil
	prepare := recordsRequest(http.MethodPost, "/business/audit-event-exports", `{"filters":{"event":"order.completed"},"format":"csv"}`, nil)
	prepare.Header.Set("Idempotency-Key", "prepare-1")
	response := httptest.NewRecorder()
	handler.prepareBusinessAuditEventExport(response, prepare)
	if response.Code != http.StatusCreated || *serviceErr != nil || response.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("prepare status=%d error=%v headers=%v", response.Code, *serviceErr, response.Header())
	}
	var prepared auditapplication.BusinessAuditExportPrepared
	if err := json.Unmarshal(response.Body.Bytes(), &prepared); err != nil || prepared.DownloadToken == "" || prepared.RowCount != 1 {
		t.Fatalf("prepared=%#v err=%v body=%s", prepared, err, response.Body.String())
	}
	download := recordsRequest(http.MethodGet, "/business/audit-event-exports/downloads/token", "", map[string]string{"token": prepared.DownloadToken})
	response = httptest.NewRecorder()
	handler.downloadBusinessAuditEventExport(response, download)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" || response.Body.Len() == 0 {
		t.Fatalf("download status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if got := response.Body.String(); got == "" || containsAny(got, []string{"hidden", "secret", prepared.DownloadToken}) {
		t.Fatalf("unsafe download=%q", got)
	}
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
