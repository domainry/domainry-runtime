package integrationmodel

import "regexp"

const ConnectorIdentityKeyMaximumLength = 128

var connectorIdentityKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

// ValidConnectorIdentityKey reports whether a Connector, Provider, Operation,
// or Connection key is a stable code-like identity suitable for persistence
// and contract references. Display names are localized separately.
func ValidConnectorIdentityKey(key string) bool {
	return connectorIdentityKeyPattern.MatchString(key)
}
