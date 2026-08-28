package operations

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

type operationsLeaseReleaseBody struct {
	ExpectedLeaseOwner   string `json:"expected_lease_owner"`
	ExpectedFencingToken int64  `json:"expected_fencing_token"`
	VerifiedStuck        bool   `json:"verified_stuck"`
	VerificationEvidence string `json:"verification_evidence"`
	Reason               string `json:"reason"`
	Reference            string `json:"reference"`
}

func (h *OperationsHandler) forceReleaseLease(w http.ResponseWriter, r *http.Request) {
	if h.leases == nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindInternal, "backend.operations.lease_control_unavailable", nil, nil))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, nil))
		return
	}
	var body operationsLeaseReleaseBody
	if h.decodeJSON == nil || !h.decodeJSON(w, r, &body) {
		return
	}
	result, err := h.leases.ForceRelease(r.Context(), operationsapplication.OperationsLeaseReleaseCommand{
		Owner: strings.TrimSpace(r.PathValue("owner")), ResourceID: strings.TrimSpace(r.PathValue("resourceID")),
		ExpectedLeaseOwner: body.ExpectedLeaseOwner, ExpectedFencingToken: body.ExpectedFencingToken,
		VerifiedStuck: body.VerifiedStuck, VerificationEvidence: body.VerificationEvidence,
		Reason: body.Reason, Reference: body.Reference,
	}, key, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", result.Receipt.StatusURL)
	h.writeJSON(w, http.StatusOK, result)
}
