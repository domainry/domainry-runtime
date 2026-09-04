package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type operationsHTTPDeadLetterOwner struct{}

func (operationsHTTPDeadLetterOwner) Inspect(_ context.Context, id string, _ principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	return operationsapplication.OperationsDeadLetterItem{
		Owner: "queue", ID: id, ResourceType: "delivery", Status: "failed",
		AllowedActions: []string{operationsapplication.OperationsDeadLetterAck},
	}, nil
}

func (operationsHTTPDeadLetterOwner) Act(_ context.Context, id, action, _, _ string, _ principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	return operationsapplication.OperationsDeadLetterItem{
		Owner: "queue", ID: id, ResourceType: "delivery", Status: action,
		AllowedActions: []string{operationsapplication.OperationsDeadLetterAck},
	}, nil
}

type operationsHTTPBreakGlassAlert struct{}

func (operationsHTTPBreakGlassAlert) BreakGlassAlert(context.Context, string, operationsmodel.OperationsBreakGlassGrant, principalmodel.Principal) error {
	return nil
}

type operationsHTTPLegacyControl struct{}

func (operationsHTTPLegacyControl) IdempotencyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error) {
	return []idempotency.ReceiptSummary{{Owner: "record", ID: "receipt-1"}}, nil
}

func (operationsHTTPLegacyControl) RetryIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error {
	return nil
}

func (operationsHTTPLegacyControl) ResetIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error {
	return nil
}

func TestOperationsRecoveryHandlersExecuteThroughHTTP(t *testing.T) {
	mux := newOperationsRecoveryHTTPMux(t)
	catalog := operationsRequest(t, mux, http.MethodGet, "/operations/catalog", "", nil, http.StatusOK)
	if catalog["count"].(float64) == 0 {
		t.Fatalf("catalog=%#v", catalog)
	}
	receipts := operationsRequest(t, mux, http.MethodGet, "/operations/idempotency/receipts?status=failed&limit=10", "", nil, http.StatusOK)
	if receipts["count"].(float64) != 1 {
		t.Fatalf("receipts=%#v", receipts)
	}
	operationsRequest(t, mux, http.MethodPost, "/operations/idempotency/receipts/record/receipt-1/retry", "receipt-retry", nil, http.StatusOK)
	operationsRequest(t, mux, http.MethodPost, "/operations/idempotency/receipts/record/receipt-1/reset", "receipt-reset", nil, http.StatusOK)

	inspect := operationsRequest(t, mux, http.MethodGet, "/operations/dead-letters/queue/dl-1", "", nil, http.StatusOK)
	if inspect["id"] != "dl-1" {
		t.Fatalf("inspect=%#v", inspect)
	}
	action := operationsRequest(t, mux, http.MethodPost, "/operations/dead-letters/queue/dl-1/ack", "dead-letter-1", map[string]any{"reason": "reviewed"}, http.StatusOK)
	if action["item"].(map[string]any)["status"] != "ack" {
		t.Fatalf("action=%#v", action)
	}

	dryRun := operationsRequest(t, mux, http.MethodPost, "/operations/dead-letters/bulk/dry-run", "bulk-dry-run", map[string]any{
		"owner": "queue", "action": "ack", "filter": map[string]any{"ids": []string{"dl-2", "dl-1"}},
		"limit": 2, "reason": "bounded recovery",
	}, http.StatusOK)
	if len(dryRun["candidates"].([]any)) != 2 || dryRun["confirmation_token"] == "" {
		t.Fatalf("dry run=%#v", dryRun)
	}
	apply := operationsRequest(t, mux, http.MethodPost, "/operations/dead-letters/bulk/apply", "bulk-apply", map[string]any{
		"dry_run_operation_id": dryRun["dry_run_operation_id"], "confirmation_token": dryRun["confirmation_token"],
		"confirm": true, "reason": "approved recovery",
	}, http.StatusOK)
	if apply["succeeded"].(float64) != 2 {
		t.Fatalf("apply=%#v", apply)
	}

	diagnostics := operationsRequest(t, mux, http.MethodPost, "/operations/diagnostics/snapshots", "diagnostics-1", map[string]any{
		"sections": []string{"schema_migration"}, "reason": "health review",
	}, http.StatusOK)
	if diagnostics["snapshot"] == nil {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}

	enabled := operationsRequest(t, mux, http.MethodPost, "/operations/break-glass-grants", "break-glass-enable", map[string]any{
		"duration_seconds": 60, "approver_ids": []string{"approver-a", "approver-b"}, "reason": "incident mitigation",
		"incident_ref": "INC-1", "alert_target": "security-ops",
	}, http.StatusOK)
	grant := enabled["grant"].(map[string]any)
	grantID := grant["id"].(string)
	listed := operationsRequest(t, mux, http.MethodGet, "/operations/break-glass-grants?limit=5", "", nil, http.StatusOK)
	if listed["count"].(float64) != 1 {
		t.Fatalf("listed=%#v", listed)
	}
	disabled := operationsRequest(t, mux, http.MethodPost, "/operations/break-glass-grants/"+grantID+"/revoke", "break-glass-disable", map[string]any{
		"expected_revision": 1, "reason": "incident stabilized", "incident_ref": "INC-1",
	}, http.StatusOK)
	if disabled["grant"].(map[string]any)["state"] != "revoked" {
		t.Fatalf("disabled=%#v", disabled)
	}

	runbook := operationsRequest(t, mux, http.MethodGet, "/operations/runbooks/errors?error_code=backend.operations.lease_release_precondition_failed", "", nil, http.StatusOK)
	if runbook["category"] != "lease-and-drain" {
		t.Fatalf("runbook=%#v", runbook)
	}
	operationsRequest(t, mux, http.MethodGet, "/operations/runbooks/errors?error_code=unregistered", "", nil, http.StatusNotFound)
}

func TestOperationsRecoveryHandlersRejectInvalidInputAndUnavailableLease(t *testing.T) {
	mux := newOperationsRecoveryHTTPMux(t)
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/operations/database-retirements"},
		{http.MethodGet, "/operations/database-retirements"},
		{http.MethodGet, "/operations/database-retirements/retirement-1"},
		{http.MethodPost, "/operations/database-retirements/retirement-1/preview"},
		{http.MethodPost, "/operations/database-retirements/retirement-1/advance"},
		{http.MethodPost, "/operations/database-retirements/retirement-1/execute"},
	} {
		operationsRequest(t, mux, request.method, request.path, "", map[string]any{}, http.StatusInternalServerError)
	}

	for _, test := range []struct {
		path string
		key  string
	}{
		{path: "/operations/dead-letters/queue/dl-1/ack", key: "dead-letter-invalid"},
		{path: "/operations/dead-letters/bulk/dry-run", key: "bulk-invalid"},
		{path: "/operations/dead-letters/bulk/apply", key: "bulk-apply-invalid"},
		{path: "/operations/diagnostics/snapshots", key: "diagnostics-invalid"},
		{path: "/operations/break-glass-grants", key: "break-glass-invalid"},
		{path: "/operations/break-glass-grants/grant-1/revoke", key: "break-glass-disable-invalid"},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader("{"))
		request.Header.Set("Idempotency-Key", test.key)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s malformed JSON status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}

	operationsRequest(t, mux, http.MethodPost, "/operations/dead-letters/bulk/dry-run", "bulk-filter-invalid", map[string]any{
		"owner": "queue", "action": "ack", "filter": map[string]any{"ids": []string{}}, "limit": 2, "reason": "invalid",
	}, http.StatusBadRequest)
	operationsRequest(t, mux, http.MethodPost, "/operations/diagnostics/snapshots", "diagnostics-section-invalid", map[string]any{
		"sections": []string{"secrets"}, "reason": "invalid",
	}, http.StatusBadRequest)
	operationsRequest(t, mux, http.MethodPost, "/operations/break-glass-grants", "break-glass-command-invalid", map[string]any{
		"duration_seconds": 0, "reason": "invalid",
	}, http.StatusBadRequest)
	for _, path := range []string{
		"/operations/dead-letters/queue/dl-1",
		"/operations/break-glass-grants",
		"/operations/missing-operation",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Deny", "true")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPut, "/operations/controls/maintenance/runtime", strings.NewReader("{"))
	request.Header.Set("Idempotency-Key", "control-invalid-json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("control malformed JSON status=%d body=%s", response.Code, response.Body.String())
	}

	noLease := newOperationsRecoveryHTTPMuxWithLease(t, false)
	operationsRequest(t, noLease, http.MethodPost, "/operations/leases/worker/job-1/force-release", "", map[string]any{}, http.StatusInternalServerError)

	withLease := newOperationsRecoveryHTTPMuxWithLease(t, true)
	operationsRequest(t, withLease, http.MethodPost, "/operations/leases/worker/job-1/force-release", "", map[string]any{}, http.StatusBadRequest)
	request = httptest.NewRequest(http.MethodPost, "/operations/leases/worker/job-1/force-release", strings.NewReader("{"))
	request.Header.Set("Idempotency-Key", "lease-invalid-json")
	response = httptest.NewRecorder()
	withLease.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("lease malformed JSON status=%d body=%s", response.Code, response.Body.String())
	}
	operationsRequest(t, withLease, http.MethodPost, "/operations/leases/worker/job-1/force-release", "lease-precondition", map[string]any{
		"expected_lease_owner": "instance-a", "expected_fencing_token": 1, "verified_stuck": true,
		"verification_evidence": "lease expired", "reason": "operator recovery",
	}, http.StatusConflict)
}

func TestOwnerOperationHeadersAndReason(t *testing.T) {
	response := httptest.NewRecorder()
	WriteOwnerReceiptHeaders(response, operationsapplication.OperationsOwnerExecutionResult{})
	if response.Header().Get("Operation-ID") != "" {
		t.Fatalf("blank receipt headers=%v", response.Header())
	}
	result := operationsapplication.OperationsOwnerExecutionResult{Replayed: true, Receipt: operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "operation-1"}, StatusURL: "/operations/operation-1"}}
	WriteOwnerReceiptHeaders(response, result)
	if response.Header().Get("Operation-ID") != "operation-1" || response.Header().Get("Location") != "/operations/operation-1" || response.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("receipt headers=%v", response.Header())
	}
	request := httptest.NewRequest(http.MethodPost, "/operations", nil)
	request.Header.Set("X-Operation-Reason", " explicit reason ")
	if reason := OwnerOperationReason(request, "fallback"); reason != "explicit reason" {
		t.Fatalf("reason=%q", reason)
	}
	if reason := OwnerOperationReason(nil, "fallback"); reason != "fallback" {
		t.Fatalf("nil request reason=%q", reason)
	}
}

func newOperationsRecoveryHTTPMux(t *testing.T) *http.ServeMux {
	return newOperationsRecoveryHTTPMuxWithLease(t, true)
}

func newOperationsRecoveryHTTPMuxWithLease(t *testing.T, includeLease bool) *http.ServeMux {
	t.Helper()
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-recovery-http.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := operationspersistence.NewOperationsStore(runtimeStore)
	fixedNow := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	nextID := 0
	service := operationsapplication.NewOperationsApplicationService(repository, operationsHTTPLegacyControl{}, func() time.Time { return fixedNow }, func() string {
		nextID++
		return fmt.Sprintf("http-recovery-%d", nextID)
	})
	if err := service.RegisterDeadLetterOwner("queue", operationsHTTPDeadLetterOwner{}); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterDiagnostics(repository, "runtime-1"); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterBreakGlass(repository, operationsHTTPBreakGlassAlert{}); err != nil {
		t.Fatal(err)
	}
	deps := OperationsDependencies{
		Service: service, Controls: operationsapplication.NewOperationsControlApplicationService(repository, service, repository, nil),
		Principal: func(r *http.Request) principalmodel.Principal {
			principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{
				operationscontract.ActionListOperations,
				operationscontract.ActionGetOperation,
				operationscontract.ActionRetryIdempotencyReceipt,
				operationscontract.ActionResetIdempotencyReceipt,
				operationscontract.ActionForceReleaseLease,
				operationscontract.ActionInspectDeadLetter,
				operationscontract.ActionResolveDeadLetter,
				operationscontract.ActionRetryDeadLetter,
				operationscontract.ActionAcknowledgeDeadLetter,
				operationscontract.ActionDryRunBulkDeadLetters,
				operationscontract.ActionApplyBulkDeadLetters,
				operationscontract.ActionCaptureDiagnostics,
				operationscontract.ActionListBreakGlass,
				operationscontract.ActionEnableBreakGlass,
				operationscontract.ActionDisableBreakGlass,
			}})
			if r.Header.Get("X-Deny") == "true" {
				accessfixture.Set(&principal, accessfixture.Bundle{})
			}
			return principal
		},
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, target any) bool {
			if err := json.NewDecoder(r.Body).Decode(target); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return false
			}
			return true
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			case apperror.KindConflict:
				status = http.StatusConflict
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": apperror.CodeOf(err)})
		},
	}
	if includeLease {
		deps.Leases = operationsapplication.NewOperationsLeaseApplicationService(repository, service, func() time.Time { return fixedNow })
	}
	handler := NewOperationsHandler(deps)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux
}
