package transport

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type taskKnowledgeProbe struct {
	result                         knowledgesdk.Result
	authorities                    []knowledgesdk.Authority
	searches, reads, revalidations int
	err, revalidateErr             error
}

func (p *taskKnowledgeProbe) SearchKnowledge(context.Context, string, knowledgesdk.Authority) (knowledgesdk.Result, error) {
	return knowledgesdk.Result{}, errors.New("unscoped source must never be used")
}
func (p *taskKnowledgeProbe) ReadKnowledge(context.Context, string, knowledgesdk.Authority) (knowledgesdk.Result, error) {
	return knowledgesdk.Result{}, errors.New("unscoped source must never be used")
}
func (p *taskKnowledgeProbe) SearchLibraryKnowledge(_ context.Context, _, _ string, a knowledgesdk.Authority) (knowledgesdk.Result, error) {
	p.searches++
	p.authorities = append(p.authorities, a)
	return p.result, p.err
}
func (p *taskKnowledgeProbe) ReadLibraryKnowledge(_ context.Context, _, _ string, a knowledgesdk.Authority) (knowledgesdk.Result, error) {
	p.reads++
	p.authorities = append(p.authorities, a)
	return p.result, p.err
}
func (p *taskKnowledgeProbe) RevalidateKnowledge(_ context.Context, _ knowledgesdk.Result, a knowledgesdk.Authority) error {
	p.revalidations++
	p.authorities = append(p.authorities, a)
	return p.revalidateErr
}
func taskKnowledgeFixture() (*taskKnowledgeProbe, principalmodel.Principal) {
	p := &taskKnowledgeProbe{result: knowledgesdk.Result{LibraryID: "library-a", Operation: "search", Query: "Eco", ScopeSHA256: strings.Repeat("a", 64), Data: json.RawMessage(`{"provider_secret":"never-release-raw"}`), Citations: []knowledgesdk.Citation{{ID: "citation-a", Provider: "knowledge", LibraryID: "library-a", Operation: "search", DocumentID: "doc-a", DocumentVersion: 3, ChunkID: "chunk-a", Title: "Eco models", Excerpt: "Authorized passage", URL: "https://docs.example.test/eco"}}}}
	return p, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "service-a", RoleKey: "agent_service"}}
}
func TestTaskKnowledgeUsesCurrentIdentityLibraryAndNormalizedCitationsOnly(t *testing.T) {
	p, principal := taskKnowledgeFixture()
	adapter := agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}
	result, err := adapter.SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal)
	if err != nil || p.searches != 1 || p.revalidations != 1 {
		t.Fatal(result, err)
	}
	encoded, _ := json.Marshal(result.Output)
	if strings.Contains(string(encoded), "never-release-raw") || strings.Contains(string(encoded), "provider_secret") || !strings.Contains(string(encoded), "document_version") || !strings.Contains(string(encoded), "Authorized passage") {
		t.Fatal("raw payload released or citation evidence lost", string(encoded))
	}
	for _, a := range p.authorities {
		if !a.Known || a.RuntimeID != "runtime-a" || a.WorkspaceID != principal.WorkspaceID || a.UserID != principal.UserID || a.RoleKey != principal.RoleKey {
			t.Fatal("model replaced live identity", a)
		}
	}
	p.result.Operation, p.result.Query, p.result.DocumentID = "fetch", "", "doc-a"
	p.result.Citations[0].Operation = "fetch"
	if _, err = adapter.ReadAgentKnowledge(t.Context(), "library-a", "doc-a", principal); err != nil || p.reads != 1 || p.revalidations != 2 {
		t.Fatal("fetch did not revalidate document", err)
	}
	p.revalidateErr = &knowledgesdk.Error{Class: knowledgesdk.ErrorClassForbidden, Code: "revoked"}
	value, err := adapter.ReadAgentKnowledge(t.Context(), "library-a", "doc-a", principal)
	if apperror.CodeOf(err) != "agent.tool.knowledge_denied" || value.Output != nil || value.Knowledge != nil {
		t.Fatal("revoked result escaped", value, err)
	}
}
func TestTaskKnowledgeRejectsMismatchedOrUnprovenSourceResults(t *testing.T) {
	for _, change := range []func(*taskKnowledgeProbe){
		func(p *taskKnowledgeProbe) { p.result.LibraryID = "foreign" }, func(p *taskKnowledgeProbe) { p.result.Operation = "fetch" },
		func(p *taskKnowledgeProbe) { p.result.Query = "other query" }, func(p *taskKnowledgeProbe) { p.result.ScopeSHA256 = "" },
		func(p *taskKnowledgeProbe) { p.result.Citations[0].LibraryID = "foreign" }, func(p *taskKnowledgeProbe) { p.result.Citations[0].ID = "" },
		func(p *taskKnowledgeProbe) { p.result.Citations = make([]knowledgesdk.Citation, 21) },
	} {
		p, principal := taskKnowledgeFixture()
		change(p)
		value, err := (agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal)
		if value.Output != nil || value.Knowledge != nil || apperror.CodeOf(err) != "agent.tool.knowledge_result_invalid" {
			t.Fatal("unproven source released", value, err)
		}
	}
}
func TestTaskKnowledgeNoConfigurationNoIdentityAndNoHitsRemainDistinct(t *testing.T) {
	p, principal := taskKnowledgeFixture()
	if _, err := (agentTaskKnowledgeAdapter{}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal); apperror.CodeOf(err) != "agent.tool.knowledge_unavailable" {
		t.Fatal(err)
	}
	principal.Known = false
	if _, err := (agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal); apperror.CodeOf(err) != "agent.tool.knowledge_identity_denied" || p.searches != 0 {
		t.Fatal(err)
	}
	principal.Known = true
	p.result.Citations = nil
	result, err := (agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal)
	if err != nil || len(result.Output.(agentTaskKnowledgeResult).Citations) != 0 || p.revalidations != 1 {
		t.Fatal("empty search fabricated a citation", result, err)
	}
	for _, class := range []knowledgesdk.ErrorClass{knowledgesdk.ErrorClassBadRequest, knowledgesdk.ErrorClassForbidden, knowledgesdk.ErrorClassNotFound, knowledgesdk.ErrorClassConflict, knowledgesdk.ErrorClassRateLimited, knowledgesdk.ErrorClassUnavailable} {
		p.err = &knowledgesdk.Error{Class: class, Code: "upstream-error"}
		if result, err := (agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal); err == nil || result.Output != nil || result.Knowledge != nil {
			t.Fatal("failed search became empty successful evidence", result, err)
		}
	}
}

func TestTaskKnowledgeReferenceReceiptContainsNoSourceTextOrAuthority(t *testing.T) {
	p, principal := taskKnowledgeFixture()
	p.result.Citations[0].RecordID = "record-a"
	p.result.Citations[0].ContentSHA256 = strings.Repeat("b", 64)
	p.result.Citations[0].SourceRevision = "generation-a"
	result, err := (agentTaskKnowledgeAdapter{source: p, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", principal)
	if err != nil || result.Knowledge == nil || len(result.Knowledge.Citations) != 1 {
		t.Fatal(result, err)
	}
	citation := result.Knowledge.Citations[0]
	if citation.DocumentID != "doc-a" || citation.DocumentVersion != 3 || citation.RecordID != "record-a" || citation.ChunkID != "chunk-a" || citation.SourceRevision != "generation-a" {
		t.Fatal("actual citation references lost", citation)
	}
	raw, _ := json.Marshal(result.Knowledge)
	for _, forbidden := range []string{"Authorized passage", "Eco models", "https://docs.example.test", "never-release-raw", "authorization", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("receipt included source content or authority", string(raw))
		}
	}
	for _, change := range []func(*taskKnowledgeProbe){func(p *taskKnowledgeProbe) { p.result.ScopeSHA256 = "unproven" }, func(p *taskKnowledgeProbe) { p.result.Citations[0].DocumentVersion = -1 }, func(p *taskKnowledgeProbe) { p.result.Citations[0].ContentSHA256 = "invalid" }} {
		source, who := taskKnowledgeFixture()
		change(source)
		r, err := (agentTaskKnowledgeAdapter{source: source, runtimeID: "runtime-a"}).SearchAgentKnowledge(t.Context(), "library-a", "Eco", who)
		if err == nil || r.Knowledge != nil || r.Output != nil {
			t.Fatal("invalid receipt released", r, err)
		}
	}
}
