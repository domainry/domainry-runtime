package policy

import "github.com/domainry/domainry-foundation/secrets"

// RedactSensitiveMap returns a deep copy with credential-shaped values masked.
func RedactSensitiveMap(value map[string]any) map[string]any {
	return secrets.RedactMap(value)
}

// IsSensitiveKey reports whether a field name carries credential-shaped data.
func IsSensitiveKey(key string) bool {
	return secrets.IsSensitiveKey(key)
}
