package agenthost

import (
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	knowledge "github.com/domainry/domainry-knowledge-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestConversationKnowledgeAuthorizationUsesCurrentActorAndLibraryOwnerFacts(t *testing.T) {
	host, resolver, _, actor := newConversationBusinessFixture(t)
	action, found, err := knowledge.AuthorizationAction(knowledge.ActionDocumentsDownload)
	if err != nil || !found || action.Permission == nil {
		t.Fatal("Knowledge download action missing", err)
	}
	permission := action.Permission
	resolver.principal = accessfixture.Attach(resolver.principal, accessfixture.Bundle{Key: actor.RoleKey,
		Permissions: []string{permission.Key}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: permission.ResourceKey, Action: permission.OperationKey, Scope: identity.DataScopeOwner, Read: true}}})
	library := agent.KnowledgeLibrary{ID: "lib_source", Kind: "shared", Role: "reader", OwnerUserID: actor.UserID}
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), knowledge.ActionDocumentsDownload, library, actor); err != nil {
		t.Fatal("independent file read policy required execution authority", err)
	}
	if resolver.request.SubjectID != identity.SubjectID(actor.UserID) || resolver.request.RoleKey != actor.RoleKey || resolver.workspace != actor.WorkspaceID {
		t.Fatal("library authorization lost exact current actor", resolver.request)
	}
	library.OwnerUserID = "another_source_owner"
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), knowledge.ActionDocumentsDownload, library, actor); err == nil {
		t.Fatal("owner file policy evaluated with reader as resource owner")
	}
	library.OwnerUserID = actor.UserID
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), knowledge.ActionLibrariesGet, library, actor); err == nil {
		t.Fatal("download permission granted unrelated library metadata")
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), actor.UserID, actor); err != nil {
		t.Fatal("active workspace member rejected", err)
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), "foreign_workspace_member", actor); err == nil {
		t.Fatal("unresolved member admitted")
	}
	resolver.revoke()
	if err := host.AuthorizeKnowledgeLibrary(t.Context(), knowledge.ActionDocumentsDownload, library, actor); err == nil {
		t.Fatal("saved download policy survived current actor withdrawal")
	}
	if err := host.ValidateKnowledgeLibraryMember(t.Context(), actor.UserID, actor); err == nil {
		t.Fatal("inactive actor admitted member")
	}
}

func TestConversationKnowledgeAuthorizationResolvesLatestOwnerActionsAndRejectsAliases(t *testing.T) {
	actions, err := knowledge.AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		t.Run(action.Key, func(t *testing.T) {
			host, resolver, _, actor := newConversationBusinessFixture(t)
			library := agent.KnowledgeLibrary{ID: "library", Kind: "shared", Role: "manager", OwnerUserID: actor.UserID}
			if action.Permission == nil {
				if err := host.AuthorizeKnowledgeLibrary(t.Context(), action.Key, library, actor); err == nil {
					t.Fatal("worker Action admitted as user permission")
				}
				return
			}
			permission := action.Permission
			resolver.principal = accessfixture.Attach(resolver.principal, accessfixture.Bundle{Key: actor.RoleKey, Permissions: []string{permission.Key}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: permission.ResourceKey, Action: permission.OperationKey, Scope: identity.DataScopeAll, Read: true, Write: true}}})
			if err := host.AuthorizeKnowledgeLibrary(t.Context(), action.Key, library, actor); err != nil {
				t.Fatal("current source-owned action not supported", err)
			}
		})
	}
	host, _, _, actor := newConversationBusinessFixture(t)
	for _, unknown := range []string{"documents_download", "libraries_get", "knowledge.documents.unknown", "customer.read"} {
		if err := host.AuthorizeKnowledgeLibrary(t.Context(), unknown, agent.KnowledgeLibrary{}, actor); err == nil {
			t.Fatal("unknown or obsolete operation alias admitted", unknown)
		}
	}
}

func TestConversationKnowledgeCurrentVersionedToolDefinitionsAreAuthorized(t *testing.T) {
	host, resolver, _, actor := newConversationBusinessFixture(t)
	definitions := append(agent.LibraryKnowledgeConversationTools(), agent.KnowledgeExtractionTool())
	definitions = append(definitions, agent.AttachmentConversationTools()...)
	bundle := accessfixture.Bundle{Key: actor.RoleKey}
	for _, d := range definitions {
		bundle.Permissions = append(bundle.Permissions, d.ActionKey)
		bundle.DataPolicies = append(bundle.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "agent.conversation_tools", Action: d.Key, Scope: identity.DataScopeAll, Read: true})
	}
	resolver.principal = accessfixture.Attach(resolver.principal, bundle)
	for _, d := range definitions {
		auth, err := host.AuthorizeConversationTool(t.Context(), agent.ConversationToolRequest{Authority: actor, Definition: d})
		if err != nil || !auth.Granted {
			t.Fatal("current knowledge/attachment tool unavailable", d.Key, d.Version, err)
		}
	}
}

func TestConversationKnowledgeDocumentSourceUsesCurrentRecordReadPolicy(t *testing.T) {
	host, _, _, actor := newConversationBusinessFixture(t)
	source := agent.KnowledgeDocumentSourceAccess{
		Namespace: knowledge.KnowledgeDocumentSourceNamespaceRuntimeRecord, ResourceType: "customer", ResourceID: "own-1",
	}
	if err := host.AuthorizeKnowledgeDocumentSource(t.Context(), source, actor); err != nil {
		t.Fatal("owned source record rejected", err)
	}
	source.ResourceID = "other-owner"
	if err := host.AuthorizeKnowledgeDocumentSource(t.Context(), source, actor); err == nil {
		t.Fatal("foreign source record authorized")
	}
	source.ResourceID = "own-1"
	source.Namespace = "untrusted"
	if err := host.AuthorizeKnowledgeDocumentSource(t.Context(), source, actor); err == nil {
		t.Fatal("unknown source namespace authorized")
	}
}
