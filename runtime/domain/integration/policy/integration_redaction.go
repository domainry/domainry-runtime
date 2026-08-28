package policy

import "github.com/domainry/domainry-runtime/runtime/platform/secrets"

// RedactSensitiveMap owns Integration payload redaction. Integration must not
// depend on Audit Service as a generic credential helper.
func RedactSensitiveMap(value map[string]any) map[string]any {
	return secrets.RedactMap(value)
}
