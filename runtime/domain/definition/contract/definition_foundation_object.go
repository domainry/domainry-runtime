package contract

const (
	PartyObjectKey                    = "party"
	PersonObjectKey                   = "person"
	OrganizationObjectKey             = "organization"
	IdentityUserObjectKey             = "identity_user"
	IdentityDepartmentObjectKey       = "identity_department"
	IdentityOrganizationUnitObjectKey = "identity_organization_unit"
	IdentityWorkforceProfileObjectKey = "identity_workforce_profile"
)

// IsFoundationObjectKey reports whether objectKey is owned by Foundation or
// Identity and can therefore be referenced without being declared as a
// Runtime business metadata object.
func IsFoundationObjectKey(objectKey string) bool {
	switch objectKey {
	case PartyObjectKey, PersonObjectKey, OrganizationObjectKey,
		IdentityUserObjectKey, IdentityDepartmentObjectKey, IdentityOrganizationUnitObjectKey, IdentityWorkforceProfileObjectKey:
		return true
	default:
		return false
	}
}
