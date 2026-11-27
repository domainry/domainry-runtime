package validation

import (
	"regexp"
	"strings"

	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

var canonicalCodePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// NormalizeCanonicalCode returns the canonical representation shared by
// provisioning requests and Workspace administration targets.
func NormalizeCanonicalCode(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
}

// ValidateCanonicalCode applies the Workspace code syntax without applying
// provisioning-only reservations such as the retired "default" code.
func ValidateCanonicalCode(value string) error {
	if !canonicalCodePattern.MatchString(value) {
		return workspaceprovisionmodel.ErrInvalid
	}
	return nil
}

// NormalizeRequest canonicalizes a provisioning request before validation,
// idempotency fingerprinting, and persistence.
func NormalizeRequest(request workspaceprovisionmodel.Request) workspaceprovisionmodel.Request {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.WorkspaceCode = NormalizeCanonicalCode(request.WorkspaceCode)
	request.WorkspaceName = strings.TrimSpace(request.WorkspaceName)
	request.AdminLoginID = strings.ToLower(strings.TrimSpace(request.AdminLoginID))
	request.AdminName = strings.TrimSpace(request.AdminName)
	return request
}

// ValidateRequest owns the complete provisioning request invariants. Both the
// application boundary and direct host initialization use this domain rule.
func ValidateRequest(request workspaceprovisionmodel.Request) error {
	if request.RequestID == "" || ValidateCanonicalCode(request.WorkspaceCode) != nil || strings.EqualFold(request.WorkspaceCode, "default") ||
		request.WorkspaceName == "" ||
		request.AdminLoginID == "" || request.AdminName == "" {
		return workspaceprovisionmodel.ErrInvalid
	}
	return nil
}
