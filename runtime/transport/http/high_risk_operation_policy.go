package http

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

const (
	operationReasonHeader       = "X-Operation-Reason"
	operationConfirmationHeader = "X-Operation-Confirmation"
	operationConfirmedValue     = "confirmed"
	operationBreakGlassValue    = "break-glass"
)

// withHighRiskOperationPolicy enforces the reviewed endpoint contract before
// an Operations owner can execute a mutation. Owner handlers still own
// domain-specific validation and audit payloads; this middleware prevents a
// client from bypassing the common human-reason and explicit-confirmation gate.
func (s *HTTPRouter) withHighRiskOperationPolicy(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := routePolicyFor(routes, r)
		if r.Method == http.MethodOptions || policy.fallback {
			next.ServeHTTP(w, r)
			return
		}
		contract, classified := runtimeEndpointContracts[r.Method+" "+policy.path]
		if !classified || contract.HighRiskPolicy == endpointmodel.HighRiskActionNone {
			next.ServeHTTP(w, r)
			return
		}
		reason, err := decodeOperationReasonHeader(r.Header.Get(operationReasonHeader))
		if err != nil {
			s.rejectHighRiskOperation(w, r, contract, "reason_encoding", "operations.reason_encoding_invalid")
			return
		}
		if reason == "" {
			s.rejectHighRiskOperation(w, r, contract, "reason", "operations.reason_required")
			return
		}
		r.Header.Set(operationReasonHeader, reason)
		switch contract.HighRiskPolicy {
		case endpointmodel.HighRiskActionConfirmRequired:
			if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationConfirmedValue {
				s.rejectHighRiskOperation(w, r, contract, "confirmation", "operations.confirmation_required")
				return
			}
		case endpointmodel.HighRiskActionBreakGlass:
			if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationBreakGlassValue {
				s.rejectHighRiskOperation(w, r, contract, "break_glass", "operations.break_glass_confirmation_required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
	contract endpointmodel.RuntimeEndpointContractV1,
	missing string,
	code string,
) {
	s.appendSecurityAudit(r, "high_risk_operation_rejected", "High-risk Runtime operation rejected by endpoint contract", map[string]any{
		"endpoint_identity": contract.EndpointIdentity,
		"high_risk_policy":  string(contract.HighRiskPolicy),
		"missing_evidence":  missing,
	})
	writeError(w, r, http.StatusBadRequest, code)
}
