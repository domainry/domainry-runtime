package principalmodel

import (
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type SystemScopeKind string

const (
	SystemScopeRuntimeGlobal SystemScopeKind = "runtime_global"
	SystemScopeBootstrap     SystemScopeKind = "bootstrap"
	SystemScopeInstallation  SystemScopeKind = "installation"
)

// SystemScope makes a non-tenant execution boundary explicit. It must never be
// inferred from an empty WorkspaceID.
type SystemScope struct {
	Kind    SystemScopeKind
	Purpose string
}

func NewSystemScope(kind SystemScopeKind, purpose string) SystemScope {
	return SystemScope{Kind: kind, Purpose: strings.TrimSpace(purpose)}
}

func (scope SystemScope) Valid() bool {
	switch scope.Kind {
	case SystemScopeRuntimeGlobal, SystemScopeBootstrap, SystemScopeInstallation:
		return scope.Purpose != ""
	default:
		return false
	}
}

func NewSystemPrincipal(userID string, scope SystemScope, capabilities ...string) Principal {
	return Principal{
		Principal: identitysdk.Principal{UserID: strings.TrimSpace(userID), Known: scope.Valid()}, SystemScope: scope,
		SystemCapabilities: append([]string(nil), capabilities...),
	}
}
