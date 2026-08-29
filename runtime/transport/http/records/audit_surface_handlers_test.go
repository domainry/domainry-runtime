package records

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func auditSurfaceHTTPPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "auditor"}}, accessfixture.Bundle{RecordScope: "all_records", Permissions: []string{
		"customer.read",
		auditapplication.PermissionBusinessAuditRead,
		auditapplication.PermissionTenantGovernanceRead,
		auditapplication.PermissionTenantGovernanceExport,
		auditapplication.PermissionOperationsAuditRead,
		auditapplication.PermissionOperationsAuditExport,
	}, DataPolicies: []accessfixture.DataPolicyFixture{{
		ObjectKey: "customer", Scope: "all_records", Read: true,
	}}},
	)
}

func TestAuditSurfaceHandlersProjectQueriesDetailsAndExports(t *testing.T) {
	auditRepository := &recordsAuditRepository{events: []auditmodel.AuditEvent{
		{ID: "business", Event: "order_updated", ObjectKey: "customer", RecordID: "one", ActorID: "auditor"},
		{ID: "governance", Event: "identity_role_updated", ObjectKey: "role", ActorID: "auditor"},
		{ID: "operations", Event: "scheduler_dead_letter_resolved", ObjectKey: "job_dead_letter", ActorID: "auditor"},
	}}
	recordRepository := &recordsHTTPRepository{
		page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one"}}, Total: 1},
	}
	handler, serviceErr := recordsHandlerForTest(auditSurfaceHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(auditRepository)
	handler.UseQueries(recordsHTTPApplication(recordRepository))

	cursor := auditmodel.EncodeAuditEventCursor(auditmodel.AuditEvent{ID: "audit-cursor", CreatedAt: "2026-08-18T12:00:00Z"})
	queryTarget := "/audit?object_key=%20customer%20&record_id=%20one%20&event=%20updated%20&actor_id=%20actor%20&role_key=%20role%20&request_id=%20request%20&created_from=%202026-01-01T00:00:00Z%20&created_to=%202026-12-31T00:00:00Z%20&page_size=17&cursor=" + cursor
	response := httptest.NewRecorder()
	handler.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, queryTarget, "", nil))
	if response.Code != http.StatusOK || *serviceErr != nil ||
		auditRepository.eventQuery.ObjectKey != "customer" || auditRepository.eventQuery.RecordID != "one" ||
		auditRepository.eventQuery.Event != "updated" || auditRepository.eventQuery.ActorID != "actor" ||
		auditRepository.eventQuery.RoleKey != "role" || auditRepository.eventQuery.RequestID != "request" ||
		auditRepository.eventQuery.CreatedTo != "2026-12-31T00:00:00Z" || auditRepository.eventQuery.Limit != 18 || auditRepository.eventQuery.Cursor != cursor {
		t.Fatalf("business status=%d error=%v query=%#v", response.Code, *serviceErr, auditRepository.eventQuery)
	}

	response = httptest.NewRecorder()
	handler.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, "/audit", "", nil))
	if response.Code != http.StatusOK || auditRepository.eventQuery.ActorID != "auditor" {
		t.Fatalf("actor-scoped business status=%d query=%#v", response.Code, auditRepository.eventQuery)
	}

	tests := []struct {
		name        string
		call        func(http.ResponseWriter, *http.Request)
		disposition string
	}{
		{name: "governance list", call: handler.listTenantGovernanceAuditEvents},
		{name: "governance export", call: handler.exportTenantGovernanceAuditEvents, disposition: `attachment; filename="tenant-governance-audit.json"`},
		{name: "operations list", call: handler.listOperationsAuditEvents},
		{name: "operations export", call: handler.exportOperationsAuditEvents, disposition: `attachment; filename="runtime-operations-audit.json"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.call(response, recordsRequest(http.MethodGet, "/audit?limit=5", "", nil))
			if response.Code != http.StatusOK || *serviceErr != nil || response.Header().Get("Content-Disposition") != test.disposition {
				t.Fatalf("status=%d error=%v disposition=%q", response.Code, *serviceErr, response.Header().Get("Content-Disposition"))
			}
		})
	}
}

func TestBusinessAuditHTTPReadsActionAuditFromActualStoreAndDeniesMissingPermission(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "business-audit-http.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := auditpersistence.NewRepositoryFromStore(store)
	requestID := `request-%_\literal~`
	if err := repository.InsertAuditEvent(t.Context(), "workspace", auditmodel.AuditEvent{
		ID: "action-audit", WorkspaceID: "workspace", Event: "ticket.assign", ObjectKey: "customer",
		RecordID: "one", ActorID: "manager", RoleKey: "manager", Summary: "assigned",
		Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "manager"}}, accessfixture.Bundle{Permissions: []string{"customer.read", auditapplication.PermissionBusinessAuditRead}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}}})
	recordRepository := &recordsHTTPRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one"}}, Total: 1}}
	handler, serviceErr := recordsHandlerForTest(principal)
	handler.audit = auditapplication.NewAuditApplicationService(repository)
	handler.UseQueries(recordsHTTPApplication(recordRepository))

	response := httptest.NewRecorder()
	target := "/business/audit-events?request_id=request-%25_%5Cliteral~&object_key=customer&record_id=one&limit=100"
	handler.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, target, "", nil))
	if response.Code != http.StatusOK || *serviceErr != nil {
		t.Fatalf("authorized business audit status=%d error=%v body=%s", response.Code, *serviceErr, response.Body.String())
	}
	var result auditapplication.SurfaceAuditResult[auditapplication.BusinessAuditEventDTO]
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "action-audit" {
		t.Fatalf("authorized business audit result=%+v", result)
	}

	accessfixture.Set(&principal, accessfixture.Bundle{
		Permissions:  []string{"customer.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
	})
	denied, deniedErr := recordsHandlerForTest(principal)
	denied.audit = auditapplication.NewAuditApplicationService(repository)
	denied.UseQueries(recordsHTTPApplication(recordRepository))
	denied.writeServiceError = func(w http.ResponseWriter, _ *http.Request, err error) {
		*deniedErr = err
		if apperror.KindOf(err) == apperror.KindForbidden {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}
	response = httptest.NewRecorder()
	denied.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, target, "", nil))
	if response.Code != http.StatusForbidden || apperror.CodeOf(*deniedErr) != "backend.audit.view_permission_required" {
		t.Fatalf("denied business audit status=%d error=%v", response.Code, *deniedErr)
	}
}

func TestAuditSurfaceHandlersForwardRecordAndAuditFailures(t *testing.T) {
	auditRepository := &recordsAuditRepository{}
	recordRepository := &recordsHTTPRepository{err: errors.New("record unavailable")}
	handler, serviceErr := recordsHandlerForTest(auditSurfaceHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(auditRepository)
	handler.UseQueries(recordsHTTPApplication(recordRepository))
	for _, target := range []string{"/audit?object_key=customer", "/audit?record_id=one"} {
		response := httptest.NewRecorder()
		handler.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, target, "", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("partial target=%s status=%d", target, response.Code)
		}
	}

	response := httptest.NewRecorder()
	handler.listBusinessAuditEvents(response, recordsRequest(http.MethodGet, "/audit?object_key=customer&record_id=one", "", nil))
	if response.Code != 599 || !errors.Is(*serviceErr, recordRepository.err) {
		t.Fatalf("record failure status=%d error=%v", response.Code, *serviceErr)
	}

	auditErr := errors.New("audit unavailable")
	auditRepository.err = auditErr
	recordRepository.err = nil
	for _, call := range []func(http.ResponseWriter, *http.Request){
		handler.listBusinessAuditEvents,
		handler.listTenantGovernanceAuditEvents,
		handler.exportTenantGovernanceAuditEvents,
		handler.listOperationsAuditEvents,
		handler.exportOperationsAuditEvents,
	} {
		*serviceErr = nil
		response = httptest.NewRecorder()
		call(response, recordsRequest(http.MethodGet, "/audit", "", nil))
		if response.Code != 599 || !errors.Is(*serviceErr, auditErr) {
			t.Fatalf("audit failure status=%d error=%v", response.Code, *serviceErr)
		}
	}
}
