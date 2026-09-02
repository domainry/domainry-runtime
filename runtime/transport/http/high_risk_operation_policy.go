package http

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

const (
	operationReasonHeader       = "X-Operation-Reason"
	operationConfirmationHeader = "X-Operation-Confirmation"
	operationConfirmedValue     = "confirmed"
	operationBreakGlassValue    = "break-glass"
)

// withHighRiskOperationPolicy enforces the resolved Action manifest before an
// Operations owner can execute a mutation. Runtime-generated Actions already
// project endpoint contracts, while module Actions retain source ownership;
// this keeps the common gate on the same immutable registry as authorization.
func (s *HTTPRouter) withHighRiskOperationPolicy(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := routePolicyFor(routes, r)
		if r.Method == http.MethodOptions || policy.fallback {
			next.ServeHTTP(w, r)
			return
		}
		resolved := s.resolveRequestAction(routes, r)
		if !resolved.found || len(resolved.definition.ApprovalPolicies) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		action := resolved.definition
		if actionRequiresApproval(action, actioncontract.ApprovalReason) {
			reason, err := decodeOperationReasonHeader(r.Header.Get(operationReasonHeader))
			if err != nil {
				s.rejectHighRiskOperation(w, r, action, "reason_encoding", "operations.reason_encoding_invalid")
				return
			}
			if reason == "" {
				s.rejectHighRiskOperation(w, r, action, "reason", "operations.reason_required")
				return
			}
			r.Header.Set(operationReasonHeader, reason)
		}
		if actionRequiresApproval(action, actioncontract.ApprovalConfirmation) && strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationConfirmedValue {
			s.rejectHighRiskOperation(w, r, action, "confirmation", "operations.confirmation_required")
			return
		}
		if actionRequiresApproval(action, actioncontract.ApprovalBreakGlass) && strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationBreakGlassValue {
			s.rejectHighRiskOperation(w, r, action, "break_glass", "operations.break_glass_confirmation_required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func actionRequiresApproval(action actioncontract.ActionDefinition, wanted actioncontract.ApprovalPolicy) bool {
	for _, policy := range action.ApprovalPolicies {
		if policy == wanted {
			return true
		}
	}
	return false
}

func decodeOperationReasonHeader(value string) (string, error) {
	const utf8Prefix = "UTF-8''"
	raw := strings.TrimSpace(value)
	if !strings.HasPrefix(raw, utf8Prefix) {
		return raw, nil
	}
	decoded, err := url.QueryUnescape(strings.TrimPrefix(raw, utf8Prefix))
	if err != nil || !utf8.ValidString(decoded) {
		return "", errInvalidOperationReasonEncoding
	}
	return strings.TrimSpace(decoded), nil
}

var errInvalidOperationReasonEncoding = &operationReasonEncodingError{}

type operationReasonEncodingError struct{}

func (*operationReasonEncodingError) Error() string { return "invalid UTF-8 operation reason encoding" }

func (s *HTTPRouter) rejectHighRiskOperation(
	w http.ResponseWriter,
	r *http.Request,
	action actioncontract.ActionDefinition,
	missing string,
	code string,
) {
	s.appendSecurityAudit(r, "high_risk_operation_rejected", "High-risk Runtime Action rejected by approval policy", map[string]any{
		"action_key":        action.Key,
		"approval_policies": append([]actioncontract.ApprovalPolicy(nil), action.ApprovalPolicies...),
		"missing_evidence":  missing,
	})
	writeError(w, r, http.StatusBadRequest, code)
}
