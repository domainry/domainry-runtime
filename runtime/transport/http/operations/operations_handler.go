package operations

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/idempotency"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationsService interface {
	Definitions() []operationsmodel.OperationsDefinition
	Submit(context.Context, operationsapplication.OperationsSubmitRequest, string, principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error)
	Receipt(context.Context, string, principalmodel.Principal) (operationsmodel.OperationsReceipt, error)
	Receipts(context.Context, operationsmodel.OperationsStatus, int, principalmodel.Principal) ([]operationsmodel.OperationsReceipt, error)
	SearchReceipts(context.Context, operationsmodel.OperationsReceiptFilter, principalmodel.Principal) (operationsmodel.OperationsReceiptPage, error)
	LegacyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error)
	RetryLegacyReceipt(context.Context, principalmodel.Principal, string, string) error
	ResetLegacyReceipt(context.Context, principalmodel.Principal, string, string) error
	ExecuteOwnerOperation(context.Context, operationsapplication.OperationsOwnerExecutionRequest, principalmodel.Principal, func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error)
	EnableBreakGlass(context.Context, operationsapplication.OperationsBreakGlassEnableCommand, string, principalmodel.Principal) (operationsapplication.OperationsBreakGlassResult, error)
	DisableBreakGlass(context.Context, string, operationsapplication.OperationsBreakGlassDisableCommand, string, principalmodel.Principal) (operationsapplication.OperationsBreakGlassResult, error)
	ListBreakGlass(context.Context, int, principalmodel.Principal) ([]operationsmodel.OperationsBreakGlassGrant, error)
	CaptureDiagnostics(context.Context, operationsapplication.OperationsDiagnosticsCommand, string, principalmodel.Principal) (operationsapplication.OperationsDiagnosticsResult, error)
	DryRunBulkDeadLetters(context.Context, operationsapplication.OperationsBulkDryRunRequest, string, principalmodel.Principal) (operationsapplication.OperationsBulkPlan, error)
	ApplyBulkDeadLetters(context.Context, operationsapplication.OperationsBulkApplyRequest, string, principalmodel.Principal) (operationsapplication.OperationsBulkApplyResult, error)
	InspectDeadLetter(context.Context, string, string, principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error)
	ActOnDeadLetter(context.Context, string, string, string, operationsapplication.OperationsDeadLetterActionRequest, string, principalmodel.Principal) (operationsapplication.OperationsDeadLetterActionResult, error)
}

type OperationsControlService interface {
	Set(context.Context, operationsapplication.OperationsControlRequest, string, principalmodel.Principal) (operationsapplication.OperationsControlResult, error)
	List(context.Context, operationsmodel.OperationsControlKind, int, principalmodel.Principal) ([]operationsmodel.OperationsControl, error)
}

type OperationsLeaseService interface {
	ForceRelease(context.Context, operationsapplication.OperationsLeaseReleaseCommand, string, principalmodel.Principal) (operationsapplication.OperationsLeaseReleaseReceipt, error)
}

type DatabaseRetirementService interface {
	Discover(context.Context, operationsmodel.DatabaseObjectIdentity, string, principalmodel.Principal) (operationsmodel.DatabaseRetirement, error)
	List(context.Context, operationsmodel.DatabaseRetirementState, int, principalmodel.Principal) ([]operationsmodel.DatabaseRetirement, error)
	OperationalStatus(context.Context, string, principalmodel.Principal) (operationsmodel.DatabaseRetirementOperationalStatus, error)
	Preview(context.Context, string, principalmodel.Principal) (operationsmodel.DatabaseDropPlan, error)
	Advance(context.Context, string, operationsmodel.DatabaseRetirementState, operationsmodel.DatabaseRetirementEvidence, principalmodel.Principal) (operationsmodel.DatabaseRetirement, error)
	Execute(context.Context, string, principalmodel.Principal) (operationscontract.DatabaseRetirementExecutionResult, error)
}

type OperationsHandler struct {
	service            OperationsService
	controls           OperationsControlService
	leases             OperationsLeaseService
	databaseRetirement DatabaseRetirementService
	lifecycle          *lifecycleapplication.LifecycleApplicationService
	principal          func(*http.Request) principalmodel.Principal
	writeJSON          func(http.ResponseWriter, int, any)
	writeServiceError  func(http.ResponseWriter, *http.Request, error)
	decodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	securityAudit      func(*http.Request, principalmodel.Principal, string, string, map[string]any)
	authenticated      func(http.HandlerFunc) http.HandlerFunc
}

type OperationsDependencies struct {
	Service            OperationsService
	Controls           OperationsControlService
	Leases             OperationsLeaseService
	DatabaseRetirement DatabaseRetirementService
	Lifecycle          *lifecycleapplication.LifecycleApplicationService
	Principal          func(*http.Request) principalmodel.Principal
	WriteJSON          func(http.ResponseWriter, int, any)
	WriteServiceError  func(http.ResponseWriter, *http.Request, error)
	DecodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	SecurityAudit      func(*http.Request, principalmodel.Principal, string, string, map[string]any)
	Admin              func(http.HandlerFunc) http.HandlerFunc
	Authenticated      func(http.HandlerFunc) http.HandlerFunc
}

func NewOperationsHandler(deps OperationsDependencies) *OperationsHandler {
	authenticated := deps.Authenticated
	if authenticated == nil {
		authenticated = deps.Admin
	}
	return &OperationsHandler{service: deps.Service, controls: deps.Controls, leases: deps.Leases, databaseRetirement: deps.DatabaseRetirement, lifecycle: deps.Lifecycle, principal: deps.Principal, writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON, securityAudit: deps.SecurityAudit, authenticated: authenticated}
}

func (h *OperationsHandler) receipts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	receipts, err := h.service.LegacyReceipts(r.Context(), h.principal(r), r.URL.Query().Get("status"), limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": receipts, "count": len(receipts)})
}

func (h *OperationsHandler) retry(w http.ResponseWriter, r *http.Request) { h.mutate(w, r, false) }
func (h *OperationsHandler) reset(w http.ResponseWriter, r *http.Request) { h.mutate(w, r, true) }

func (h *OperationsHandler) mutate(w http.ResponseWriter, r *http.Request, reset bool) {
	principal := h.principal(r)
	owner, receiptID := strings.TrimSpace(r.PathValue("owner")), strings.TrimSpace(r.PathValue("receiptID"))
	operation := "retry"
	if reset {
		operation = "reset"
	}
	kind := "idempotency.receipt." + operation
	result, err := h.service.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: kind, ResourceType: "idempotency_receipt", ResourceID: receiptID,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: OwnerOperationReason(r, "operator requested "+kind),
		Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")), Payload: map[string]any{"owner": owner, "receipt_id": receiptID},
	}, principal, func(ctx context.Context) (any, error) {
		var ownerErr error
		if reset {
			ownerErr = h.service.ResetLegacyReceipt(ctx, principal, owner, receiptID)
		} else {
			ownerErr = h.service.RetryLegacyReceipt(ctx, principal, owner, receiptID)
		}
		if ownerErr == nil && h.securityAudit != nil {
			h.securityAudit(r, principal, "idempotency_receipt_"+operation, "Admin changed idempotency receipt state", map[string]any{"owner": owner, "receipt_id": receiptID, "operation": operation})
		}
		return map[string]any{"ok": ownerErr == nil, "operation": operation}, ownerErr
	})
	WriteOwnerReceiptHeaders(w, result)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result.Value)
}
