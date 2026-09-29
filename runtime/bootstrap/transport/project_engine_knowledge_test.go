package transport

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type projectKnowledgeProbe struct {
	result              agentsdk.ConversationKnowledgeResult
	searchErr           error
	revalidateErr       error
	library, query      string
	searchAuthority     agentsdk.ConversationAuthority
	revalidateAuthority agentsdk.ConversationAuthority
}

func (p *projectKnowledgeProbe) SearchLibraryKnowledge(_ context.Context, libraryID, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	p.library, p.query, p.searchAuthority = libraryID, query, authority
	return p.result, p.searchErr
}
func (p *projectKnowledgeProbe) RevalidateKnowledge(_ context.Context, _ agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) error {
	p.revalidateAuthority = authority
	return p.revalidateErr
}
func (*projectKnowledgeProbe) ListKnowledgeLibraries(context.Context, string, int, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return agentsdk.ConversationKnowledgeResult{}, nil
}
func (*projectKnowledgeProbe) ReadLibraryKnowledge(context.Context, string, string, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return agentsdk.ConversationKnowledgeResult{}, nil
}
func (*projectKnowledgeProbe) SearchKnowledge(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return agentsdk.ConversationKnowledgeResult{}, nil
}
func (*projectKnowledgeProbe) ReadKnowledge(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return agentsdk.ConversationKnowledgeResult{}, nil
}

func projectKnowledgePrincipal(context.Context) (principalmodel.Principal, bool) {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "sales_rep"}}, true
}

func TestProjectEngineKnowledgeSearchUsesCurrentPrincipalAndNormalizedCitations(t *testing.T) {
	probe := &projectKnowledgeProbe{result: agentsdk.ConversationKnowledgeResult{
		LibraryID: "library-a", Provider: "managed_documents", Operation: "search", Query: "部署风险", ScopeSHA256: "scope",
		Citations: []agentsdk.ConversationCitation{{ID: "citation-a", LibraryID: "library-a", Provider: "managed_documents", Operation: "search", DocumentID: "document-a", Title: "互动", Excerpt: "客户担心迁移窗口"}},
	}}
	engine := &projectEngine{knowledge: probe, runtimeID: "aurora-runtime", principal: projectKnowledgePrincipal}
	result, err := engine.SearchKnowledgeLibrary(t.Context(), " library-a ", " 部署风险 ")
	if err != nil {
		t.Fatal(err)
	}
	if result.LibraryID != "library-a" || result.Query != "部署风险" || len(result.Citations) != 1 || result.Citations[0].DocumentID != "document-a" {
		t.Fatalf("result=%#v", result)
	}
	if probe.library != "library-a" || probe.query != "部署风险" || probe.searchAuthority.RuntimeID != "aurora-runtime" || probe.searchAuthority.WorkspaceID != "workspace-a" || probe.searchAuthority.UserID != "user-a" || probe.searchAuthority.RoleKey != "sales_rep" || probe.revalidateAuthority != probe.searchAuthority {
		t.Fatalf("probe=%#v", probe)
	}
}

func TestProjectEngineKnowledgeSearchFailsClosedOnMissingSourceOrRevalidation(t *testing.T) {
	engine := &projectEngine{runtimeID: "aurora-runtime", principal: projectKnowledgePrincipal}
	if _, err := engine.SearchKnowledgeLibrary(t.Context(), "library-a", "query"); runtimeengine.HTTPStatus(err) != 503 {
		t.Fatalf("missing source error=%v", err)
	}
	probe := &projectKnowledgeProbe{result: agentsdk.ConversationKnowledgeResult{LibraryID: "library-a", Operation: "search", Query: "query"}, revalidateErr: &agentsdk.Error{Class: "forbidden", Code: "knowledge_access_denied"}}
	engine.knowledge = probe
	if _, err := engine.SearchKnowledgeLibrary(t.Context(), "library-a", "query"); runtimeengine.HTTPStatus(err) != 403 {
		t.Fatalf("revoked search error=%v", err)
	}
}

var _ agentsdk.ConversationLibraryKnowledgeSource = (*projectKnowledgeProbe)(nil)
