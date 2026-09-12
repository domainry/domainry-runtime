package runtime

import (
	"fmt"
	"net/http"

	"github.com/domainry/domainry-agent-sdk/businessrpc"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
)

// ConversationBusinessSource borrows this Runtime's assembled owners. It does
// not open an Agent module or another database. The caller must stop all users
// of the source before closing Runtime, as with the existing module bindings.
func ConversationBusinessSource(runtime *Runtime) (businessrpc.Backend, error) {
	if runtime == nil || runtime.identityBinding == nil || runtime.identityPrincipals == nil || runtime.records == nil {
		return nil, fmt.Errorf("Runtime business owners and Identity must be assembled first")
	}
	return transport.NewConversationBusinessSource(transport.ConversationBusinessDependencies{RuntimeID: runtime.cfg.RuntimeInstanceID, Application: identity.ApplicationScope{WorkspaceID: identity.WorkspaceID(runtime.cfg.IdentityWorkspaceID), ApplicationKey: identity.ApplicationKey(runtime.cfg.IdentityAudience)}, Records: runtime.records, Principals: runtime.identityPrincipals, IntegrationSecretKey: runtime.cfg.IntegrationSecretKey, IdentityIssuer: runtime.identityBinding.Descriptor().Issuer})
}

// ConversationBusinessHandler is a separately attached service boundary; it is
// never implicitly mounted in Runtime's browser/product routes. The deploying
// service owns this application credential, listener/TLS, admission and shutdown.
// The service token is distinct from IntegrationSecretKey, which seals evidence.
func ConversationBusinessHandler(runtime *Runtime, token string) (http.Handler, error) {
	source, err := ConversationBusinessSource(runtime)
	if err != nil {
		return nil, err
	}
	return businessrpc.NewHandler(businessrpc.ServerOptions{Scope: businessrpc.Scope{RuntimeID: runtime.cfg.RuntimeInstanceID, WorkspaceID: runtime.cfg.IdentityWorkspaceID, ApplicationKey: runtime.cfg.IdentityAudience, IdentityIssuer: runtime.identityBinding.Descriptor().Issuer}, Token: token, Backend: source})
}
