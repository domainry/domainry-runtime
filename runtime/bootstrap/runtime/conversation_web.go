package runtime

import (
	"fmt"
	"io/fs"
	"net/http"

	agentweb "github.com/domainry/domainry-agent-sdk/browsergateway"
)

type ConversationWebOptions struct {
	Origin string
	Model  string
	Files  fs.FS
}

// ConversationWebHandler attaches Agent's browser UI to this Runtime's actual
// modules. It does not open another Identity/Agent database, infer a principal,
// or bypass Runtime's normal HTTP admission and authorization middleware.
// The caller owns the HTTP listener and must stop it before closing Runtime.
func ConversationWebHandler(runtime *Runtime, options ConversationWebOptions) (http.Handler, error) {
	if runtime == nil || runtime.api == nil || runtime.identityBinding == nil || runtime.agentBinding == nil {
		return nil, fmt.Errorf("Runtime Identity and conversation modules must be assembled before attaching the browser")
	}
	return agentweb.NewHandler(agentweb.Options{Identity: runtime.identityBinding, Agent: runtime.agentBinding, RuntimeID: runtime.cfg.RuntimeInstanceID, WorkspaceID: runtime.cfg.IdentityWorkspaceID, ApplicationKey: runtime.cfg.IdentityAudience, Origin: options.Origin, Model: options.Model, Files: options.Files, ConversationHandler: runtime.Routes()})
}
