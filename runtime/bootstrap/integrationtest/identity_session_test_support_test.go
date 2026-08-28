package integrationtest

import (
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// runtimeIdentityFixtureSession is issued by the Identity SDK fixture. Runtime
// integration tests deliberately do not call /auth: authentication HTTP
// surfaces belong to the selected Identity module/service, not to Runtime.
func runtimeIdentityFixtureSession(t *testing.T, login, role string) identitysdk.AuthSession {
	t.Helper()
	login = strings.TrimSpace(login)
	role = strings.TrimSpace(role)
	if login == "" || role == "" {
		t.Fatalf("identity fixture session requires login and role")
	}
	return identitysdk.AuthSession{
		SessionID:    "plane-testkit-session",
		WorkspaceID:  "default",
		AccessToken:  integrationIdentityAccessTokenFor(login, role),
		RefreshToken: integrationIdentityAccessTokenFor(login, role),
		TokenType:    "Bearer",
		User:         identitysdk.User{ID: login, Status: "active"},
		Roles:        []identitysdk.Role{{ID: role, Key: role, Status: "active"}},
		DefaultRole:  role,
	}
}
