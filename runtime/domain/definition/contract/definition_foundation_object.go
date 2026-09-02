package contract

const (
	IdentityUserObjectKey             = "identity_user"
	IdentityOrganizationUnitObjectKey = "identity_organization_unit"
)

// IsFoundationObjectKey reports whether objectKey is owned by Foundation or
// Identity and can therefore be referenced without being declared as a
// Runtime business metadata object.
func IsFoundationObjectKey(objectKey string) bool {
	switch objectKey {
	case IdentityUserObjectKey, IdentityOrganizationUnitObjectKey:
		return true
	default:
		return false
	}
}
