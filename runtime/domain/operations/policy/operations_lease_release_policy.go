package policy

import (
	"fmt"
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func OperationsValidateLeaseReleaseRequest(request operationsmodel.OperationsLeaseReleaseRequest) error {
	if strings.TrimSpace(request.Owner) == "" || strings.TrimSpace(request.ResourceID) == "" || strings.TrimSpace(request.ExpectedLeaseOwner) == "" || request.ExpectedFencingToken <= 0 || request.Now.IsZero() {
		return fmt.Errorf("backend.operations.lease_release_identity_required")
	}
	if request.VerifiedStuck && strings.TrimSpace(request.VerificationEvidence) == "" {
		return fmt.Errorf("backend.operations.lease_release_evidence_required")
	}
	return nil
}
