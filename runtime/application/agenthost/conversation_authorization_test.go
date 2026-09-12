package agenthost

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationExecutionAuthorizationUsesCurrentRuntimePrincipal(t *testing.T) {
	host, resolver, _, a := newConversationBusinessFixture(t)
	in := agentsdk.ConversationExecutionAuthorizationRequest{Authority: a, ConversationID: "conversation", RunID: "run", Stage: "execute"}
	if allowed, err := host.AuthorizeConversationExecution(t.Context(), in); err != nil || !allowed {
		t.Fatal("current authenticated owner denied", err)
	}
	if resolver.request.SubjectID != "operator" || resolver.request.RoleKey != a.RoleKey || resolver.request.Application.WorkspaceID != host.application.WorkspaceID {
		t.Fatal("current principal not resolved in host application")
	}
	for _, change := range []string{"runtime", "workspace", "user", "known"} {
		forged := in
		switch change {
		case "runtime":
			forged.Authority.RuntimeID = "another-runtime"
		case "workspace":
			forged.Authority.WorkspaceID = "another-workspace"
		case "user":
			forged.Authority.UserID = "another-user"
		case "known":
			forged.Authority.Known = false
		}
		if allowed, _ := host.AuthorizeConversationExecution(t.Context(), forged); allowed {
			t.Fatalf("forged %s accepted", change)
		}
	}
	resolver.revoke()
	if allowed, _ := host.AuthorizeConversationExecution(t.Context(), in); allowed {
		t.Fatal("queued identity snapshot survived current principal revocation")
	}
}
