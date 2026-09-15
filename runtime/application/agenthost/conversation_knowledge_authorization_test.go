package agenthost

import (
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestConversationKnowledgeAuthorizationUsesCurrentActorAndLibraryOwnerFacts(t *testing.T) {
	host, resolver, _, actor := newConversationBusinessFixture(t)
	permission := agent.KnowledgeDocumentPermission("documents_download")
	resolver.principal = accessfixture.Attach(resolver.principal, accessfixture.Bundle{Key: actor.RoleKey,
		Permissions: []string{permission.Key}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: permission.ResourceKey, Action: permission.OperationKey, Scope: identity.DataScopeOwner, Read: true}}})
	library := agent.KnowledgeLibrary{ID: "lib_source", Kind: "shared", Role: "reader", OwnerUserID: actor.UserID}
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), "documents_download", library, actor); err != nil {
		t.Fatal("independent file read policy required execution authority", err)
	}
	if resolver.request.SubjectID != identity.SubjectID(actor.UserID) || resolver.request.RoleKey != actor.RoleKey || resolver.workspace != actor.WorkspaceID {
		t.Fatal("library authorization lost exact current actor", resolver.request)
	}
	library.OwnerUserID = "another_source_owner"
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), "documents_download", library, actor); err == nil {
		t.Fatal("owner file policy evaluated with reader as resource owner")
	}
	library.OwnerUserID = actor.UserID
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), "libraries_get", library, actor); err == nil {
		t.Fatal("download permission granted unrelated library metadata")
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), actor.UserID, actor); err != nil {
		t.Fatal("active workspace member rejected", err)
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), "foreign_workspace_member", actor); err == nil {
		t.Fatal("unresolved member admitted")
	}
	resolver.revoke()
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), "documents_download", library, actor); err == nil {
		t.Fatal("saved download policy survived current actor withdrawal")
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), actor.UserID, actor); err == nil {
		t.Fatal("inactive actor admitted member")
	}
}

func TestConversationKnowledgeCurrentVersionedToolDefinitionsAreAuthorized(t *testing.T) {
	host, resolver, _, actor := newConversationBusinessFixture(t)
	definitions := append(agent.LibraryKnowledgeConversationTools(), agent.KnowledgeExtractionTool())
	bundle := accessfixture.Bundle{Key: actor.RoleKey}
	for _, d := range definitions {
		bundle.Permissions = append(bundle.Permissions, d.ActionKey)
		bundle.DataPolicies = append(bundle.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "agent.conversation_tools", Action: d.Key, Scope: identity.DataScopeAll, Read: true})
	}
	resolver.principal = accessfixture.Attach(resolver.principal, bundle)
	for _, d := range definitions {
		auth, err := host.AuthorizeConversationTool(t.Context(), agent.ConversationToolRequest{Authority: actor, Definition: d})
		if err != nil || !auth.Granted {
			t.Fatal("current library/extraction tool unavailable", d.Key, d.Version, err)
		}
	}
}
