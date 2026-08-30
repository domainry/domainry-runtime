package records

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type recordsBusinessAuditExporter struct{}

func (*recordsBusinessAuditExporter) ConfigureExport([]byte, auditmodel.ExportAuthorizer) {}
func (*recordsBusinessAuditExporter) PrepareExport(context.Context, auditmodel.ExportRequest, string, auditmodel.ExportPrincipal) (auditmodel.ExportPrepared, error) {
	return auditmodel.ExportPrepared{ID: "export-1", ReportSource: "business_audit_events", Filename: "audit.csv", ContentSHA256: "hash", RowCount: 1, AuditIdentity: "identity", ScopeSHA256: "scope", DownloadToken: "token-token-token-token-token-token-token-token-token-token-token-token-token-token-token-token", ExpiresAt: "2026-08-30T00:00:00Z"}, nil
}
func (*recordsBusinessAuditExporter) DownloadExport(context.Context, string, auditmodel.ExportPrincipal) ([]byte, string, error) {
	return []byte("audit_id,event\naudit-1,order.completed\n"), "audit.csv", nil
}

func TestBusinessAuditExportHandlersRejectUnknownFiltersAndReturnServerBytes(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "auditor"}}, accessfixture.Bundle{
		Key: "business-auditor", RecordScope: "all_records", Permissions: []string{auditapplication.PermissionBusinessAuditRead, auditapplication.PermissionBusinessAuditExport},
	})
	repository := &recordsAuditRepository{}
	service := auditapplication.NewAuditApplicationService(repository, &recordsBusinessAuditExporter{})
	service.ConfigureBusinessExport([]byte("0123456789abcdef0123456789abcdef"), nil)
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
