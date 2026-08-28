package profilebindingmodel

type ReservedFieldOwner string

const (
	ReservedFieldOwnerAccount   ReservedFieldOwner = "identity_account"
	ReservedFieldOwnerWorkforce ReservedFieldOwner = "workforce"
)

var identityAccountOwnedProfileFields = map[string]struct{}{
	"account_status": {},
	"password":       {},
}

var identityWorkforceOwnedProfileFields = map[string]struct{}{
	"department_id":        {},
	"department_path":      {},
	"employee_no":          {},
	"employment_status":    {},
	"employment_type":      {},
	"hire_date":            {},
	"job_level":            {},
	"job_title":            {},
	"manager_ancestor_ids": {},
	"manager_depth":        {},
	"manager_id":           {},
	"manager_path":         {},
	"reporting_path":       {},
}

// ReservedFieldOwnerFor reports the external platform entity that owns a
// reserved profile field. An empty owner means the business Profile may own the
// field. Contact email/phone and gender may be valid business-Profile facts;
// gender remains business-owned until an optional Person Foundation contract
// is introduced.
func ReservedFieldOwnerFor(fieldKey string) (ReservedFieldOwner, bool) {
	if _, owned := identityAccountOwnedProfileFields[fieldKey]; owned {
		return ReservedFieldOwnerAccount, true
	}
	if _, owned := identityWorkforceOwnedProfileFields[fieldKey]; owned {
		return ReservedFieldOwnerWorkforce, true
	}
	return "", false
}
