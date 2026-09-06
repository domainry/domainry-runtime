package validation

import (
	"regexp"
	"strings"
	"time"

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

// NormalizeCommercialConfiguration canonicalizes user-authored string fields
// before validation, persistence fingerprints, and writes observe the value.
func NormalizeCommercialConfiguration(value workspaceprovisionmodel.CommercialConfiguration) workspaceprovisionmodel.CommercialConfiguration {
	value.Plan = strings.TrimSpace(value.Plan)
	value.ContractDate = strings.TrimSpace(value.ContractDate)
	value.BillingContactName = strings.TrimSpace(value.BillingContactName)
	value.BillingContactPhone = strings.TrimSpace(value.BillingContactPhone)
	value.BillingContactEmail = strings.TrimSpace(value.BillingContactEmail)
	value.BillingContactAddress = strings.TrimSpace(value.BillingContactAddress)
	value.BillingContactNotes = strings.TrimSpace(value.BillingContactNotes)
	return value
}

// ValidateCommercialConfiguration owns the commercial invariants shared by
// initial provisioning and subsequent Workspace administration updates.
func ValidateCommercialConfiguration(configuration workspaceprovisionmodel.CommercialConfiguration) error {
	if configuration.Plan == "" ||
		configuration.IncludedUserLimit < 0 || configuration.MaxUserLimit < configuration.IncludedUserLimit ||
		configuration.IncludedCustomerLimit < 0 || configuration.MaxCustomerLimit < configuration.IncludedCustomerLimit ||
		configuration.IncludedStoreLimit < 1 || configuration.MaxStores < configuration.IncludedStoreLimit ||
		configuration.BillingDay < 1 || configuration.BillingDay > 31 {
		return workspaceprovisionmodel.ErrInvalid
	}
	if _, err := time.Parse("2006-01-02", configuration.ContractDate); err != nil {
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
	request.FirstStoreCode = NormalizeCanonicalCode(request.FirstStoreCode)
	request.FirstStoreName = strings.TrimSpace(request.FirstStoreName)
	request.AdminLoginID = strings.ToLower(strings.TrimSpace(request.AdminLoginID))
	request.AdminName = strings.TrimSpace(request.AdminName)
	request.CommercialConfiguration = NormalizeCommercialConfiguration(request.CommercialConfiguration)
	return request
}

// ValidateRequest owns the complete provisioning request invariants. Both the
// application boundary and direct host initialization use this domain rule.
func ValidateRequest(request workspaceprovisionmodel.Request) error {
	if request.RequestID == "" || ValidateCanonicalCode(request.WorkspaceCode) != nil || strings.EqualFold(request.WorkspaceCode, "default") ||
		request.WorkspaceName == "" || ValidateCanonicalCode(request.FirstStoreCode) != nil || request.FirstStoreName == "" ||
		request.AdminLoginID == "" || request.AdminName == "" {
		return workspaceprovisionmodel.ErrInvalid
	}
	return ValidateCommercialConfiguration(request.CommercialConfiguration)
}
