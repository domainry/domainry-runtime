package profilebindingmodel

type ReservedFieldOwner string

const (
	ReservedFieldOwnerIdentityUser ReservedFieldOwner = "identity_user"
)

var identityUserOwnedProfileFields = map[string]struct{}{
	"account_status":  {},
	"password":        {},
	"org_id":          {},
	"manager_user_id": {},
	"reporting_path":  {},
	"worker_no":       {},
	"worker_type":     {},
	"work_status":     {},
	"start_date":      {},
	"end_date":        {},
}

// ReservedFieldOwnerFor reports the external platform entity that owns a
// reserved profile field. An empty owner means the business Profile may own the
// field. Contact email/phone and gender may be valid business-Profile facts;
// gender remains business-owned until an optional Person Foundation contract
// is introduced.
func ReservedFieldOwnerFor(fieldKey string) (ReservedFieldOwner, bool) {
	if _, owned := identityUserOwnedProfileFields[fieldKey]; owned {
		return ReservedFieldOwnerIdentityUser, true
	}
	return "", false
}
