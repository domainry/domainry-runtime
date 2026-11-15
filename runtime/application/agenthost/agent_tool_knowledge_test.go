package agenthost

import (
	"context"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// Exercise the actual source-owned visibility policy, not a prefiltered stub.
type knowledgeProjectedSchema struct{ *agentSchemaProviderStub }

func (p knowledgeProjectedSchema) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return appschemaservice.SnapshotForPrincipal(p.full, principal)
}

type knowledgeGatewayProbe struct {
	calls          int
	library, value string
	principal      principalmodel.Principal
	output         any
	wait           bool
}

func probeKnowledgeReceipt(library, value string) *agentmodel.AgentTaskKnowledgeEvidence {
	operation := "search"
	if value == "doc-a" {
		operation = "fetch"
	}
	return &agentmodel.AgentTaskKnowledgeEvidence{LibraryID: library, Operation: operation, ScopeSHA256: strings.Repeat("a", 64), Citations: []agentmodel.AgentTaskKnowledgeCitationEvidence{}}
}

func (p *knowledgeGatewayProbe) SearchAgentKnowledge(ctx context.Context, library, value string, principal principalmodel.Principal) (AgentToolKnowledgeResult, error) {
	p.calls++
	p.library, p.value, p.principal = library, value, principal
	if p.wait {
		<-ctx.Done()
		return AgentToolKnowledgeResult{}, ctx.Err()
	}
	if p.output != nil {
		return AgentToolKnowledgeResult{Output: p.output, Knowledge: probeKnowledgeReceipt(library, value)}, nil
	}
	return AgentToolKnowledgeResult{Output: map[string]any{"citations": []any{}}, Knowledge: probeKnowledgeReceipt(library, value)}, nil
}
func (p *knowledgeGatewayProbe) ReadAgentKnowledge(ctx context.Context, library, value string, principal principalmodel.Principal) (AgentToolKnowledgeResult, error) {
	return p.SearchAgentKnowledge(ctx, library, value, principal)
}

func knowledgeGatewayFixture(t *testing.T) (*AgentToolGateway, *knowledgeGatewayProbe, AgentToolInvocationRequest, *agentSchemaProviderStub, *agentPrincipalResolverStub) {
	t.Helper()
	auth, initiator, schema, directory := agentAuthorizationFixture()
	tools := []string{agentsdk.AgentToolKnowledgeSearch, agentsdk.AgentToolKnowledgeRead}
	auth.schema = knowledgeProjectedSchema{schema}
	schema.full.AgentTasks[0].AllowedObjects = nil
	schema.full.Agents[0].Tools = tools
	schema.full.Skills[0].AllowedTools = tools
	schema.full.AgentTasks[0].SideEffectMode = agentsdk.AgentTaskSideEffectAnalysisOnly
	schema.full.AgentTasks[0].AllowedActions = nil
	schema.full.AgentTasks[0].ExecutionLimits = agentsdk.AgentExecutionLimits{MaxToolCalls: 4, TimeoutSeconds: 1, MaxInputBytes: 1024, MaxOutputBytes: 4096}
	credentials := NewAgentTaskCredentialApplicationService([]byte(strings.Repeat("k", 32)), nil, nil)
	token, err := credentials.Issue(t.Context(), AgentTaskCredentialClaims{WorkspaceID: initiator.WorkspaceID, ProcessID: "process-a", TaskRunID: "task-a", AllowedTools: tools, Principal: agentsdk.PrincipalReference{UserID: initiator.UserID, RoleKey: initiator.RoleKey, WorkspaceID: initiator.WorkspaceID}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	probe := &knowledgeGatewayProbe{}
	gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: auth, Credentials: credentials, Knowledge: probe})
	request := AgentToolInvocationRequest{Credential: token, WorkspaceID: initiator.WorkspaceID, ProcessID: "process-a", TaskRunID: "task-a", Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: "customer.review", TaskVersion: "1.0.0", Tool: agentsdk.AgentToolKnowledgeSearch, Input: map[string]any{"library_id": " library-a ", "query": " Eco "}}
	return gateway, probe, request, schema, directory
}

func TestKnowledgeTaskGatewayReauthorizesEveryCallAndKeepsIdentityServerOwned(t *testing.T) {
	g, p, request, schema, directory := knowledgeGatewayFixture(t)
	result, err := g.Invoke(t.Context(), request)
	if err != nil || result.Status != "executed" || p.calls != 1 || p.library != "library-a" || p.value != "Eco" || p.principal.AuthorizationRevision != "operator-rev-2" {
		t.Fatal("current-authority read failed", result, p, err)
	}
	request.Tool, request.Input = agentsdk.AgentToolKnowledgeRead, map[string]any{"library_id": "library-a", "document_id": "doc-a"}
	if _, err = g.Invoke(t.Context(), request); err != nil || p.calls != 2 || p.value != "doc-a" {
		t.Fatal("document read failed", p, err)
	}
	schema.full.Agents[0].Tools = []string{agentsdk.AgentToolQueryRecords}
	schema.full.Skills[0].AllowedTools = []string{agentsdk.AgentToolQueryRecords}
	if _, err = g.Invoke(t.Context(), request); err == nil || p.calls != 2 {
		t.Fatal("old credential bypassed current Tool removal", err)
	}
	schema.full.Agents[0].Tools = []string{agentsdk.AgentToolKnowledgeRead}
	schema.full.Skills[0].AllowedTools = []string{agentsdk.AgentToolKnowledgeRead}
	directory.principals["operator:operator"] = principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
	if _, err = g.Invoke(t.Context(), request); err == nil || p.calls != 2 {
		t.Fatal("revoked principal reached Knowledge", err)
	}
}

func TestKnowledgeTaskGatewayRejectsAuthorityInjectionAndMalformedArguments(t *testing.T) {
	for _, input := range []map[string]any{
		{"library_id": "library-a", "query": "Eco", "permission_ids": []any{"admin"}},
		{"library_id": "library-a", "query": "Eco", "workspace_id": "foreign"},
		{"library_id": "library-a", "query": "Eco", "object_key": "customer"},
		{"library_id": 7, "query": "Eco"}, {"library_id": "library-a", "query": 7},
		{"library_id": "", "query": "Eco"}, {"library_id": "library-a", "query": " "},
		{"library_id": "library-a", "document_id": "doc-a"},
	} {
		g, p, request, _, _ := knowledgeGatewayFixture(t)
		request.Input = input
		if _, err := g.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.knowledge_input_invalid" || p.calls != 0 {
			t.Fatal("malformed or authority input reached Knowledge", input, err)
		}
	}
}

func TestKnowledgeTaskGatewayRejectsOtherCredentialScopeBeforeReading(t *testing.T) {
	for _, change := range []func(*AgentToolInvocationRequest){
		func(r *AgentToolInvocationRequest) { r.WorkspaceID = "foreign" }, func(r *AgentToolInvocationRequest) { r.TaskRunID = "other-task" },
		func(r *AgentToolInvocationRequest) { r.ProcessID = "other-process" }, func(r *AgentToolInvocationRequest) { r.Credential = "invalid" },
	} {
		g, p, request, _, _ := knowledgeGatewayFixture(t)
		change(&request)
		if _, err := g.Invoke(t.Context(), request); err == nil || p.calls != 0 {
			t.Fatal("credential expanded scope", err)
		}
	}
}

func TestKnowledgeTaskGatewayRetainsTimeoutAndOutputBounds(t *testing.T) {
	g, p, request, _, _ := knowledgeGatewayFixture(t)
	p.wait = true
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := g.Invoke(ctx, request); apperror.CodeOf(err) != "agent.tool.timeout" {
		t.Fatal("Knowledge timeout became successful evidence", err)
	}
	g, p, request, _, _ = knowledgeGatewayFixture(t)
	p.output = strings.Repeat("x", 5000)
	if _, err := g.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.output_invalid" {
		t.Fatal("Knowledge bypassed Task output budget", err)
	}
	g, _, request, _, _ = knowledgeGatewayFixture(t)
	g.dependencies.Knowledge = nil
	if _, err := g.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.knowledge_unavailable" {
		t.Fatal("unconfigured Knowledge fabricated a result", err)
	}
}
