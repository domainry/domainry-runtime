package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

type agentToolLedgerStub struct {
	starts    []agentrepository.AgentToolCallStart
	finishes  int
	beginErr  error
	finishErr error
}

func (s *agentToolLedgerStub) BeginAgentToolCall(_ context.Context, start agentrepository.AgentToolCallStart) (string, int, error) {
	s.starts = append(s.starts, start)
	return "call-1", len(s.starts), s.beginErr
}
func (s *agentToolLedgerStub) FinishAgentToolCall(context.Context, agentrepository.AgentToolCallFinish) error {
	s.finishes++
	return s.finishErr
}

type agentToolQueryStub struct {
	fields    []string
	principal principalmodel.Principal
	output    any
	err       error
}

func (s *agentToolQueryStub) QueryAgentRecords(_ context.Context, object string, _ map[string]any, scope AgentToolFieldScope, principal principalmodel.Principal) (any, error) {
	s.fields, s.principal = scope.VisibleFields[object], principal
	if s.output != nil || s.err != nil {
		return s.output, s.err
	}
	return []any{map[string]any{"name": "Ada"}}, nil
}
func (s *agentToolQueryStub) GetAgentRecord(_ context.Context, object, record string, scope AgentToolFieldScope, principal principalmodel.Principal) (any, error) {
	s.fields, s.principal = scope.VisibleFields[object], principal
	return map[string]any{"id": record, "name": "Ada"}, nil
}

type agentToolActionStub struct {
	calls  int
	output AgentToolActionInvocationResult
	err    error
}

func (s *agentToolActionStub) InvokeAgentAction(context.Context, AgentToolActionInvocation) (AgentToolActionInvocationResult, error) {
	s.calls++
	if s.output.Record != nil || s.output.Object != nil || s.err != nil {
		return s.output, s.err
	}
	return AgentToolActionInvocationResult{Record: map[string]any{"updated": true}}, nil
}

type agentToolProposalStub struct {
	calls int
	last  AgentToolProposalRequest
	err   error
}

func (s *agentToolProposalStub) CreateAgentActionProposal(_ context.Context, request AgentToolProposalRequest) (AgentToolProposalResult, error) {
	s.calls++
	s.last = request
	return AgentToolProposalResult{ProposalID: "proposal-1", Value: map[string]any{"proposal_id": "proposal-1"}}, s.err
}

type agentToolRiskStub struct {
	proposal bool
	err      error
}

func (s agentToolRiskStub) RequiresAgentProposal(context.Context, string, principalmodel.Principal) (bool, string, error) {
	return s.proposal, "test", s.err
}

type agentToolLimiterStub struct {
	decision ratelimit.Decision
	err      error
}

func (s agentToolLimiterStub) Allow(context.Context, string, int, time.Duration) (ratelimit.Decision, error) {
	return s.decision, s.err
}

func agentToolGatewayFixture(t *testing.T, tool string) (*AgentToolGateway, AgentToolInvocationRequest, *agentToolLedgerStub, *agentToolQueryStub, *agentToolActionStub, *agentToolProposalStub) {
	t.Helper()
	authorization, initiator, _, _ := agentAuthorizationFixture()
	if schema, ok := authorization.schema.(*agentSchemaProviderStub); ok && tool == AgentToolGetRecord {
		schema.full.Agents[0].Tools = append(schema.full.Agents[0].Tools, AgentToolGetRecord)
		for key, snapshot := range schema.filtered {
			snapshot.Agents[0].Tools = append(snapshot.Agents[0].Tools, AgentToolGetRecord)
			schema.filtered[key] = snapshot
		}
	}
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	credentials := NewAgentTaskCredentialApplicationService([]byte(strings.Repeat("k", 32)), agentTaskClock{now: now}, agentCredentialIDStub{})
	token, err := credentials.Issue(t.Context(), AgentTaskCredentialClaims{WorkspaceID: "workspace-1", ProcessID: "process-1", TaskRunID: "run-1", Principal: agentsdk.PrincipalReference{UserID: "operator", RoleKey: "operator", WorkspaceID: "workspace-1"}, AllowedTools: []string{tool}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ledger, queries, actions, proposals := &agentToolLedgerStub{}, &agentToolQueryStub{}, &agentToolActionStub{}, &agentToolProposalStub{}
	run, _, _ := runningAgentTaskRun(now)
	run.ID, run.WorkspaceID, run.ProcessID, run.NodeInstanceID, run.Lease.Owner, run.Lease.FencingToken = "run-1", "workspace-1", "process-1", "node-1", "worker", 1
	repository := &agentTaskRunRepositoryStub{current: run}
	taskRuns := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now})
	gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Credentials: credentials, Queries: queries, Actions: actions, Proposals: proposals, Risk: agentToolRiskStub{}, Ledger: ledger, TaskRuns: taskRuns})
	request := AgentToolInvocationRequest{Credential: token, WorkspaceID: "workspace-1", ProcessID: "process-1", TaskRunID: "run-1", Owner: workerplatform.WorkerID("worker"), FencingToken: 1, Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: "customer.review", TaskVersion: "1.0.0", NodeAllowedObjects: []string{"customer"}, NodeAllowedActions: []string{"customer.update"}, NodeAllowedOutcomes: []string{"success"}, Tool: tool, IdempotencyKey: "action-1"}
	return gateway, request, ledger, queries, actions, proposals
}

func TestAgentToolGatewayReauthorizesAndProjectsVisibleFields(t *testing.T) {
	gateway, request, ledger, queries, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
	request.Input = map[string]any{"object_key": "customer", "query": map[string]any{"limit": 10}}
	result, err := gateway.Invoke(t.Context(), request)
	if err != nil || result.Status != "executed" || result.CallRef != "call-1" || len(ledger.starts) != 1 || ledger.finishes != 1 {
		t.Fatalf("result=%#v ledger=%#v err=%v", result, ledger, err)
	}
	if len(queries.fields) != 1 || queries.fields[0] != "name" || queries.principal.AuthorizationRevision != "operator-rev-2" {
		t.Fatalf("fields=%v principal=%#v", queries.fields, queries.principal)
	}
	if ledger.starts[0].Authorization.AuthorizationRevision != "operator-rev-2" || ledger.starts[0].InputHash == "" {
		t.Fatalf("evidence=%#v", ledger.starts[0])
	}
}

func TestAgentToolGatewayActionModeAndRiskCannotBeLowered(t *testing.T) {
	t.Run("direct allowed", func(t *testing.T) {
		gateway, request, _, _, actions, proposals := agentToolGatewayFixture(t, AgentToolInvokeAction)
		request.Input = map[string]any{"action_key": "customer.update", "object_key": "customer", "record_id": "1", "data": map[string]any{"name": "Ada"}}
		result, err := gateway.Invoke(t.Context(), request)
		if err != nil || result.Status != "executed" || actions.calls != 1 || proposals.calls != 0 {
			t.Fatalf("result=%#v actions=%d proposals=%d err=%v", result, actions.calls, proposals.calls, err)
		}
	})
	t.Run("platform risk forces proposal", func(t *testing.T) {
		gateway, request, _, _, actions, proposals := agentToolGatewayFixture(t, AgentToolInvokeAction)
		gateway.dependencies.Risk = agentToolRiskStub{proposal: true}
		request.Input = map[string]any{"action_key": "customer.update", "object_key": "customer", "record_id": "1"}
		result, err := gateway.Invoke(t.Context(), request)
		repository := gateway.dependencies.TaskRuns.repository.(*agentTaskRunRepositoryStub)
		if err != nil || result.Status != "proposal_required" || actions.calls != 0 || proposals.calls != 1 || proposals.last.NodeInstanceID != "node-1" || proposals.last.Attempt != 1 || repository.current.Status != agentmodel.AgentTaskRunWaitingApproval || repository.current.Approval.ProposalID != "proposal-1" {
			t.Fatalf("result=%#v actions=%d proposals=%d err=%v", result, actions.calls, proposals.calls, err)
		}
	})
	t.Run("analysis only denies", func(t *testing.T) {
		gateway, request, ledger, _, actions, _ := agentToolGatewayFixture(t, AgentToolInvokeAction)
		auth := gateway.dependencies.Authorization
		schema := auth.schema.(*agentSchemaProviderStub)
		schema.full.AgentTasks[0].SideEffectMode = agentsdk.AgentTaskSideEffectAnalysisOnly
		filtered := schema.filtered["operator:operator"]
		filtered.AgentTasks[0].SideEffectMode = agentsdk.AgentTaskSideEffectAnalysisOnly
		schema.filtered["operator:operator"] = filtered
		request.Input = map[string]any{"action_key": "customer.update", "object_key": "customer"}
		_, err := gateway.Invoke(t.Context(), request)
		if apperror.CodeOf(err) != "agent.tool.write_denied" || actions.calls != 0 || ledger.finishes != 1 {
			t.Fatalf("err=%v actions=%d finishes=%d", err, actions.calls, ledger.finishes)
		}
	})
}

func TestAgentToolGatewayRejectsCredentialCapabilityAndObjectEscalation(t *testing.T) {
	gateway, request, ledger, _, _, _ := agentToolGatewayFixture(t, "delete_database")
	request.Input = map[string]any{"sql": "DROP TABLE customer"}
	if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.not_allowed" || len(ledger.starts) != 0 {
		t.Fatalf("arbitrary tool err=%v starts=%d", err, len(ledger.starts))
	}

	gateway, request, ledger, _, _, _ = agentToolGatewayFixture(t, AgentToolQueryRecords)
	request.Input = map[string]any{"object_key": "invoice"}
	if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.object_denied" || ledger.finishes != 1 {
		t.Fatalf("object escalation err=%v finishes=%d", err, ledger.finishes)
	}

	gateway, request, _, _, _, _ = agentToolGatewayFixture(t, AgentToolQueryRecords)
	request.NodeAllowedObjects = []string{"invoice"}
	request.Input = map[string]any{"object_key": "invoice"}
	if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.authorization.node_allowlist_widening" {
		t.Fatalf("node widening err=%v", err)
	}
}

func TestAgentToolGatewayEnforcesBudgetsTimeoutAndWriteIdempotency(t *testing.T) {
	t.Run("rate limit before ledger", func(t *testing.T) {
		gateway, request, ledger, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		gateway.dependencies.RateLimiter = agentToolLimiterStub{decision: ratelimit.Decision{Allowed: false}}
		request.Input = map[string]any{"object_key": "customer"}
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.rate_limited" || len(ledger.starts) != 0 {
			t.Fatalf("err=%v starts=%d", err, len(ledger.starts))
		}
	})
	t.Run("limiter failure", func(t *testing.T) {
		failure := errors.New("limiter unavailable")
		gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		gateway.dependencies.RateLimiter = agentToolLimiterStub{err: failure}
		request.Input = map[string]any{"object_key": "customer"}
		if _, err := gateway.Invoke(t.Context(), request); !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("input size", func(t *testing.T) {
		gateway, request, ledger, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		request.Input = map[string]any{"object_key": "customer", "payload": strings.Repeat("x", 70*1024)}
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.input_invalid" || len(ledger.starts) != 0 {
			t.Fatalf("err=%v starts=%d", err, len(ledger.starts))
		}
	})
	t.Run("output size", func(t *testing.T) {
		gateway, request, ledger, queries, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		queries.output = map[string]any{"content": strings.Repeat("x", 70*1024)}
		request.Input = map[string]any{"object_key": "customer"}
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.output_invalid" || ledger.finishes != 1 {
			t.Fatalf("err=%v finishes=%d", err, ledger.finishes)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		gateway, request, ledger, queries, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		queries.err = context.DeadlineExceeded
		request.Input = map[string]any{"object_key": "customer"}
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.timeout" || ledger.finishes != 1 {
			t.Fatalf("err=%v finishes=%d", err, ledger.finishes)
		}
	})
	t.Run("write idempotency", func(t *testing.T) {
		gateway, request, ledger, _, actions, _ := agentToolGatewayFixture(t, AgentToolInvokeAction)
		request.IdempotencyKey = ""
		request.Input = map[string]any{"action_key": "customer.update", "object_key": "customer", "record_id": "one"}
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.idempotency_required" || actions.calls != 0 || ledger.finishes != 1 {
			t.Fatalf("err=%v actions=%d finishes=%d", err, actions.calls, ledger.finishes)
		}
	})
}

func TestAgentToolGatewayDependencyCredentialAndLedgerBoundaries(t *testing.T) {
	base, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
	request.Input = map[string]any{"object_key": "customer"}
	for name, gateway := range map[string]*AgentToolGateway{
		"nil":           nil,
		"authorization": NewAgentToolGateway(AgentToolGatewayDependencies{}),
		"credentials":   NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: base.dependencies.Authorization}),
		"ledger":        NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: base.dependencies.Authorization, Credentials: base.dependencies.Credentials}),
	} {
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.gateway_unavailable" {
			t.Fatalf("%s unavailable=%v", name, err)
		}
	}
	badCredential := request
	badCredential.Credential = "invalid"
	if _, err := base.Invoke(t.Context(), badCredential); apperror.CodeOf(err) != "agent.credential.malformed" {
		t.Fatalf("credential=%v", err)
	}
	badAuthorization := request
	badAuthorization.NodeAllowedObjects = []string{"invoice"}
	if _, err := base.Invoke(t.Context(), badAuthorization); err == nil {
		t.Fatal("expected authorization error")
	}
	for name, mutate := range map[string]func(*agentsdk.PrincipalReference){
		"user": func(p *agentsdk.PrincipalReference) { p.UserID = "other" },
		"role": func(p *agentsdk.PrincipalReference) { p.RoleKey = "other" },
	} {
		principal := agentsdk.PrincipalReference{UserID: "operator", RoleKey: "operator", WorkspaceID: request.WorkspaceID}
		mutate(&principal)
		token, issueErr := base.dependencies.Credentials.Issue(t.Context(), AgentTaskCredentialClaims{WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskRunID: request.TaskRunID, Principal: principal, AllowedTools: []string{request.Tool}}, time.Minute)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		candidate := request
		candidate.Credential = token
		if _, err := base.Invoke(t.Context(), candidate); err == nil {
			t.Fatalf("principal %s accepted", name)
		}
	}
	marshal := request
	marshal.Input = map[string]any{"object_key": "customer", "bad": make(chan int)}
	if _, err := base.Invoke(t.Context(), marshal); apperror.CodeOf(err) != "agent.tool.input_invalid" {
		t.Fatalf("marshal=%v", err)
	}
	for name, setup := range map[string]func(*AgentToolGateway){
		"allowed limiter": func(g *AgentToolGateway) {
			g.dependencies.RateLimiter = agentToolLimiterStub{decision: ratelimit.Decision{Allowed: true}}
		},
		"begin": func(g *AgentToolGateway) { g.dependencies.Ledger.(*agentToolLedgerStub).beginErr = errors.New("begin") },
	} {
		gateway, candidate, _, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		candidate.Input = request.Input
		setup(gateway)
		result, err := gateway.Invoke(t.Context(), candidate)
		if name == "allowed limiter" && (err != nil || result.Status != "executed") {
			t.Fatalf("limiter result=%#v err=%v", result, err)
		}
		if name == "begin" && err == nil {
			t.Fatal("expected begin error")
		}
	}
	gateway, candidate, ledger, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
	candidate.Input = request.Input
	ledger.finishErr = errors.New("finish")
	if _, err := gateway.Invoke(t.Context(), candidate); !errors.Is(err, ledger.finishErr) {
		t.Fatalf("finish error=%v", err)
	}
}

func TestAgentToolGatewayTimeoutAndProposalLifecycleBoundaries(t *testing.T) {
	gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
	schema := gateway.dependencies.Authorization.schema.(*agentSchemaProviderStub)
	schema.full.AgentTasks[0].ExecutionLimits.TimeoutSeconds = 1
	for key, snapshot := range schema.filtered {
		snapshot.AgentTasks[0].ExecutionLimits.TimeoutSeconds = 1
		schema.filtered[key] = snapshot
	}
	request.Input = map[string]any{"object_key": "customer"}
	if result, err := gateway.Invoke(t.Context(), request); err != nil || result.Status != "executed" {
		t.Fatalf("timeout-configured result=%#v err=%v", result, err)
	}

	baseInput := map[string]any{"action_key": "customer.update", "object_key": "customer", "record_id": "one"}
	for name, setup := range map[string]func(*AgentToolGateway, *agentToolLedgerStub, *agentToolProposalStub, *agentTaskRunRepositoryStub){
		"proposal missing": func(g *AgentToolGateway, _ *agentToolLedgerStub, _ *agentToolProposalStub, _ *agentTaskRunRepositoryStub) {
			g.dependencies.Proposals = nil
		},
		"task runs missing": func(g *AgentToolGateway, _ *agentToolLedgerStub, _ *agentToolProposalStub, _ *agentTaskRunRepositoryStub) {
			g.dependencies.TaskRuns = nil
		},
		"initial load": func(_ *AgentToolGateway, _ *agentToolLedgerStub, _ *agentToolProposalStub, r *agentTaskRunRepositoryStub) {
			r.getErr = errors.New("load")
		},
		"initial missing": func(_ *AgentToolGateway, _ *agentToolLedgerStub, _ *agentToolProposalStub, r *agentTaskRunRepositoryStub) {
			r.current = agentmodel.AgentTaskRun{}
		},
		"proposal error": func(_ *AgentToolGateway, _ *agentToolLedgerStub, p *agentToolProposalStub, _ *agentTaskRunRepositoryStub) {
			p.err = errors.New("proposal")
		},
		"finish error": func(_ *AgentToolGateway, l *agentToolLedgerStub, _ *agentToolProposalStub, _ *agentTaskRunRepositoryStub) {
			l.finishErr = errors.New("finish")
		},
		"approval save": func(_ *AgentToolGateway, _ *agentToolLedgerStub, _ *agentToolProposalStub, r *agentTaskRunRepositoryStub) {
			r.saveRunningErr = errors.New("save")
		},
	} {
		candidate, invocation, ledger, _, _, proposals := agentToolGatewayFixture(t, AgentToolInvokeAction)
		candidate.dependencies.Risk = agentToolRiskStub{proposal: true}
		invocation.Input = baseInput
		repository, _ := candidate.dependencies.TaskRuns.repository.(*agentTaskRunRepositoryStub)
		setup(candidate, ledger, proposals, repository)
		if _, err := candidate.Invoke(t.Context(), invocation); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
	for name, setup := range map[string]func(*agentTaskRunRepositoryStub){
		"reload error":   func(r *agentTaskRunRepositoryStub) { r.getErrAfter = 2; r.getErr = errors.New("reload") },
		"reload missing": func(r *agentTaskRunRepositoryStub) { r.missingAfter = 2 },
	} {
		candidate, invocation, _, _, _, _ := agentToolGatewayFixture(t, AgentToolInvokeAction)
		candidate.dependencies.Risk = agentToolRiskStub{proposal: true}
		invocation.Input = baseInput
		repository := candidate.dependencies.TaskRuns.repository.(*agentTaskRunRepositoryStub)
		setup(repository)
		if _, err := candidate.Invoke(t.Context(), invocation); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
	if got := agentToolString(map[string]any{"value": nil}, "value"); got != "" {
		t.Fatalf("nil tool string=%q", got)
	}
}

func TestAgentToolGatewayReadActionAndOutputBoundaryMatrix(t *testing.T) {
	for name, configure := range map[string]func(*AgentToolGateway, *AgentToolInvocationRequest, *agentToolQueryStub){
		"query unavailable": func(g *AgentToolGateway, r *AgentToolInvocationRequest, _ *agentToolQueryStub) {
			g.dependencies.Queries = nil
			r.Input = map[string]any{"object_key": "customer"}
		},
		"query error": func(_ *AgentToolGateway, r *AgentToolInvocationRequest, q *agentToolQueryStub) {
			q.err = errors.New("query")
			r.Input = map[string]any{"object_key": "customer"}
		},
		"output marshal": func(_ *AgentToolGateway, r *AgentToolInvocationRequest, q *agentToolQueryStub) {
			q.output = make(chan int)
			r.Input = map[string]any{"object_key": "customer"}
		},
	} {
		gateway, request, _, queries, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
		configure(gateway, &request, queries)
		if _, err := gateway.Invoke(t.Context(), request); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
	for name, input := range map[string]map[string]any{
		"valid":  {"object_key": "customer", "record_id": "one"},
		"object": {"object_key": "invoice", "record_id": "one"},
		"record": {"object_key": "customer"},
	} {
		gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolGetRecord)
		request.Input = input
		result, err := gateway.Invoke(t.Context(), request)
		if name == "valid" && (err != nil || result.Status != "executed") {
			t.Fatalf("get=%#v err=%v", result, err)
		}
		if name != "valid" && apperror.CodeOf(err) != "agent.tool.record_denied" {
			t.Fatalf("get %s=%v", name, err)
		}
	}
	gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolGetRecord)
	gateway.dependencies.Queries = nil
	request.Input = map[string]any{"object_key": "customer", "record_id": "one"}
	if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.query_unavailable" {
		t.Fatalf("get unavailable=%v", err)
	}
	for name, input := range map[string]map[string]any{
		"action": {"action_key": "other", "object_key": "customer"},
		"object": {"action_key": "customer.update", "object_key": "invoice"},
	} {
		gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolInvokeAction)
		request.Input = input
		if _, err := gateway.Invoke(t.Context(), request); apperror.CodeOf(err) != "agent.tool.action_denied" {
			t.Fatalf("%s denied=%v", name, err)
		}
	}
	for name, setup := range map[string]func(*AgentToolGateway){
		"risk missing":   func(g *AgentToolGateway) { g.dependencies.Risk = nil },
		"risk error":     func(g *AgentToolGateway) { g.dependencies.Risk = agentToolRiskStub{err: errors.New("risk")} },
		"action missing": func(g *AgentToolGateway) { g.dependencies.Actions = nil },
	} {
		gateway, request, _, _, _, _ := agentToolGatewayFixture(t, AgentToolInvokeAction)
		request.Input = map[string]any{"action_key": "customer.update", "object_key": "customer"}
		setup(gateway)
		if _, err := gateway.Invoke(t.Context(), request); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
	if maxAgentToolCalls(agentsdk.AgentTaskDefinition{}) != 20 || maxAgentToolCalls(agentsdk.AgentTaskDefinition{ExecutionLimits: agentsdk.AgentExecutionLimits{MaxToolCalls: 3}}) != 3 {
		t.Fatal("tool call defaults")
	}
	for tool, want := range map[string]int{AgentToolInvokeAction: 5, AgentToolQueryRecords: 2, AgentToolGetRecord: 1} {
		if agentToolCostUnits(tool) != want {
			t.Fatalf("cost %s", tool)
		}
	}
	for budget, want := range map[string]int{"low": 5, "high": 50, "medium": 20} {
		if agentTaskCostBudgetUnits(budget) != want {
			t.Fatalf("budget %s", budget)
		}
	}
	if len(agentToolMap("invalid")) != 0 || agentToolMap(map[string]any{"x": 1})["x"] != 1 {
		t.Fatal("tool map")
	}
	customGateway, customRequest, _, _, _, _ := agentToolGatewayFixture(t, AgentToolQueryRecords)
	schema := customGateway.dependencies.Authorization.schema.(*agentSchemaProviderStub)
	schema.full.Agents[0].Tools = append(schema.full.Agents[0].Tools, "custom_tool")
	for key, snapshot := range schema.filtered {
		snapshot.Agents[0].Tools = append(snapshot.Agents[0].Tools, "custom_tool")
		schema.filtered[key] = snapshot
	}
	token, err := customGateway.dependencies.Credentials.Issue(t.Context(), AgentTaskCredentialClaims{WorkspaceID: customRequest.WorkspaceID, ProcessID: customRequest.ProcessID, TaskRunID: customRequest.TaskRunID, Principal: agentsdk.PrincipalReference{UserID: "operator", RoleKey: "operator", WorkspaceID: customRequest.WorkspaceID}, AllowedTools: []string{"custom_tool"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	customRequest.Credential, customRequest.Tool, customRequest.Input = token, "custom_tool", map[string]any{}
	if _, err := customGateway.Invoke(t.Context(), customRequest); apperror.CodeOf(err) != "agent.tool.not_allowed" {
		t.Fatalf("custom tool=%v", err)
	}
}

func TestInteractiveAgentReusesToolGatewayWithLiveContextAndProposalAssociations(t *testing.T) {
	authorization, principal, schema, _ := agentAuthorizationFixture()
	principal.SurfaceKey = "business_workspace"
	schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow}
	trusted, err := authorization.ResolveGlobalContext(t.Context(), GlobalAgentContextRequest{Principal: principal, EntrypointKey: "assistant.global", Surface: principal.SurfaceKey, RouteKey: "workspace.customer", ObjectKey: "customer", RecordID: "customer-1"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &agentInteractiveRunRepositoryStub{updated: true}
	runs := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: time.Now().UTC()}, agentCredentialIDStub{})
	run, _, err := runs.Create(t.Context(), AgentInteractiveRunCreateRequest{SessionID: "session-1", EntrypointKey: trusted.EntrypointKey, IdempotencyKey: "message-1", Context: trusted, Principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	queries, proposals := &agentToolQueryStub{}, &agentToolProposalStub{}
	gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Queries: queries, Proposals: proposals, InteractiveRuns: runs})
	queryRoute := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: trusted.AgentKey, TargetVersion: "1.0.0", Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer", "query": map[string]any{"page_size": 5}}}
	queried, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: queryRoute})
	if err != nil || queried.Invocation.Status != "executed" || queried.Run.ToolCallCount != 1 || len(queried.Run.ToolInvocations) != 1 || queries.principal.AuthorizationRevision != "operator-rev-2" || len(queries.fields) != 1 || queries.fields[0] != "name" {
		t.Fatalf("queried=%#v fields=%v principal=%#v err=%v", queried, queries.fields, queries.principal, err)
	}
	proposalRoute := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteProposal, TargetKey: "customer.update", Input: map[string]any{"object_key": "customer", "record_id": "customer-1", "data": map[string]any{"name": "Ada"}}, IdempotencyKey: "proposal-1"}
	proposed, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: queried.Run, Route: proposalRoute, IdempotencyKey: proposalRoute.IdempotencyKey})
	if err != nil || proposed.Invocation.Status != "proposal_required" || proposals.calls != 1 || proposals.last.InteractiveRunID != run.ID || proposals.last.SessionID != run.SessionID || proposals.last.ContextRevision != trusted.ContextRevision {
		t.Fatalf("proposed=%#v proposal=%#v err=%v", proposed, proposals.last, err)
	}
}

func TestInteractiveToolGatewayRejectsObjectActionAndBudgetEscalation(t *testing.T) {
	authorization, principal, schema, _ := agentAuthorizationFixture()
	principal.SurfaceKey = "business_workspace"
	schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal}
	trusted, err := authorization.ResolveGlobalContext(t.Context(), GlobalAgentContextRequest{Principal: principal, EntrypointKey: "assistant.global", Surface: principal.SurfaceKey, RouteKey: "workspace.customer", ObjectKey: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &agentInteractiveRunRepositoryStub{updated: true}
	runs := NewAgentInteractiveRunApplicationService(repository, nil, nil)
	run, _, err := runs.Create(t.Context(), AgentInteractiveRunCreateRequest{SessionID: "session", EntrypointKey: trusted.EntrypointKey, IdempotencyKey: "message", Context: trusted, Principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Queries: &agentToolQueryStub{}, Proposals: &agentToolProposalStub{}, InteractiveRuns: runs})
	for name, route := range map[string]agentsdk.RouteResult{
		"object":        {RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: trusted.AgentKey, Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "invoice"}},
		"direct action": {RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: trusted.AgentKey, Input: map[string]any{"tool": AgentToolInvokeAction, "object_key": "customer"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: route, IdempotencyKey: "one"}); err == nil {
				t.Fatal("escalation accepted")
			}
		})
	}
	run.ToolInvocations = []agentmodel.AgentTaskToolInvocationEvidence{{CostUnits: 20}}
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: trusted.AgentKey, Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}}}); apperror.CodeOf(err) != "agent.task.cost_budget_exceeded" {
		t.Fatalf("budget err=%v", err)
	}
}

func interactiveToolGatewayFixture(t *testing.T) (*AgentToolGateway, agentmodel.AgentInteractiveRun, *agentToolQueryStub, *agentToolProposalStub, *agentInteractiveRunRepositoryStub) {
	t.Helper()
	authorization, principal, schema, _ := agentAuthorizationFixture()
	principal.SurfaceKey = "business_workspace"
	schema.full.Agents[0].Tools = append(schema.full.Agents[0].Tools, AgentToolGetRecord)
	schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal}
	for key, snapshot := range schema.filtered {
		snapshot.Agents[0].Tools = append(snapshot.Agents[0].Tools, AgentToolGetRecord)
		snapshot.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal}
		schema.filtered[key] = snapshot
	}
	trusted, err := authorization.ResolveGlobalContext(t.Context(), GlobalAgentContextRequest{Principal: principal, EntrypointKey: "assistant.global", Surface: principal.SurfaceKey, RouteKey: "workspace.customer", ObjectKey: "customer", RecordID: "customer-1"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &agentInteractiveRunRepositoryStub{updated: true}
	runs := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: time.Now().UTC()}, agentCredentialIDStub{})
	run, _, err := runs.Create(t.Context(), AgentInteractiveRunCreateRequest{SessionID: "session", EntrypointKey: trusted.EntrypointKey, IdempotencyKey: "message", Context: trusted, Principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	queries, proposals := &agentToolQueryStub{}, &agentToolProposalStub{}
	return NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Queries: queries, Proposals: proposals, InteractiveRuns: runs}), run, queries, proposals, repository
}

func TestInteractiveToolGatewayDependencyScopeAuthorizationAndBudgetBoundaries(t *testing.T) {
	gateway, run, _, _, _ := interactiveToolGatewayFixture(t)
	validRoute := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}}
	for name, candidate := range map[string]*AgentToolGateway{
		"nil":           nil,
		"authorization": NewAgentToolGateway(AgentToolGatewayDependencies{}),
		"runs":          NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: gateway.dependencies.Authorization}),
	} {
		if _, err := candidate.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: validRoute}); apperror.CodeOf(err) != "agent.tool.gateway_unavailable" {
			t.Fatalf("%s unavailable=%v", name, err)
		}
	}
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun){
		"status":   func(r *agentmodel.AgentInteractiveRun) { r.Status = agentmodel.AgentInteractiveRunCompleted },
		"revision": func(r *agentmodel.AgentInteractiveRun) { r.ContextRevision = "other" },
		"user":     func(r *agentmodel.AgentInteractiveRun) { r.UserID = "other" },
		"role":     func(r *agentmodel.AgentInteractiveRun) { r.RoleKey = "other" },
	} {
		candidate := run
		mutate(&candidate)
		if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: candidate, Route: validRoute}); apperror.CodeOf(err) != "agent.interactive.tool_scope_denied" {
			t.Fatalf("scope %s=%v", name, err)
		}
	}
	stale := run
	stale.ContextRevision, stale.Context.ContextRevision = "stale", "stale"
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: stale, Route: validRoute}); err == nil {
		t.Fatal("expected authorization failure")
	}
	notAllowed := validRoute
	notAllowed.Input = map[string]any{"tool": "unknown", "object_key": "customer"}
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: notAllowed}); apperror.CodeOf(err) != "agent.tool.not_allowed" {
		t.Fatalf("tool=%v", err)
	}

	schema := gateway.dependencies.Authorization.schema.(*agentSchemaProviderStub)
	schema.full.Agents[0].ExecutionLimits.MaxToolCalls = 1
	schema.full.Agents[0].ExecutionLimits.MaxInputBytes = 16
	schema.full.Agents[0].ExecutionLimits.TimeoutSeconds = 1
	for key, snapshot := range schema.filtered {
		snapshot.Agents[0].ExecutionLimits = schema.full.Agents[0].ExecutionLimits
		schema.filtered[key] = snapshot
	}
	limited := run
	limited.ToolCallCount = 1
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: limited, Route: validRoute}); apperror.CodeOf(err) != "agent.task.tool_call_limit" {
		t.Fatalf("call limit=%v", err)
	}
	large := validRoute
	large.Input["payload"] = strings.Repeat("x", 32)
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: large}); apperror.CodeOf(err) != "agent.tool.input_invalid" {
		t.Fatalf("input limit=%v", err)
	}
	marshal := validRoute
	marshal.Input = map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer", "bad": make(chan int)}
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: marshal}); apperror.CodeOf(err) != "agent.tool.input_invalid" {
		t.Fatalf("input marshal=%v", err)
	}
}

func TestInteractiveToolGatewayLimiterReadProposalOutputAndPersistenceBoundaries(t *testing.T) {
	validQuery := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}}
	for name, limiter := range map[string]agentToolLimiterStub{"error": {err: errors.New("limit")}, "denied": {decision: ratelimit.Decision{Allowed: false}}, "allowed": {decision: ratelimit.Decision{Allowed: true}}} {
		gateway, run, _, _, _ := interactiveToolGatewayFixture(t)
		gateway.dependencies.RateLimiter = limiter
		result, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: validQuery})
		if name == "allowed" && (err != nil || result.Invocation.Status != "executed") {
			t.Fatalf("allowed=%#v err=%v", result, err)
		}
		if name != "allowed" && err == nil {
			t.Fatalf("%s expected limiter error", name)
		}
	}
	for name, setup := range map[string]func(*AgentToolGateway, *agentToolQueryStub, *agentInteractiveRunRepositoryStub, *agentsdk.RouteResult){
		"query missing": func(g *AgentToolGateway, _ *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, _ *agentsdk.RouteResult) {
			g.dependencies.Queries = nil
		},
		"object empty": func(_ *AgentToolGateway, _ *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, r *agentsdk.RouteResult) {
			r.Input["object_key"] = ""
		},
		"object other": func(_ *AgentToolGateway, _ *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, r *agentsdk.RouteResult) {
			r.Input["object_key"] = "invoice"
		},
		"query error": func(_ *AgentToolGateway, q *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, _ *agentsdk.RouteResult) {
			q.err = errors.New("query")
		},
		"deadline": func(_ *AgentToolGateway, q *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, _ *agentsdk.RouteResult) {
			q.err = context.DeadlineExceeded
		},
		"output marshal": func(_ *AgentToolGateway, q *agentToolQueryStub, _ *agentInteractiveRunRepositoryStub, _ *agentsdk.RouteResult) {
			q.output = make(chan int)
		},
		"save": func(_ *AgentToolGateway, _ *agentToolQueryStub, r *agentInteractiveRunRepositoryStub, _ *agentsdk.RouteResult) {
			r.saveErr = errors.New("save")
		},
	} {
		gateway, run, query, _, repository := interactiveToolGatewayFixture(t)
		route := validQuery
		route.Input = map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}
		setup(gateway, query, repository, &route)
		if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: route}); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
	for name, input := range map[string]map[string]any{"valid": {"tool": AgentToolGetRecord, "object_key": "customer", "record_id": "one"}, "object empty": {"tool": AgentToolGetRecord, "record_id": "one"}, "object other": {"tool": AgentToolGetRecord, "object_key": "invoice", "record_id": "one"}, "record empty": {"tool": AgentToolGetRecord, "object_key": "customer"}} {
		gateway, run, _, _, _ := interactiveToolGatewayFixture(t)
		route := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: input}
		result, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: route})
		if name == "valid" && (err != nil || result.Invocation.Status != "executed") {
			t.Fatalf("get=%#v err=%v", result, err)
		}
		if name != "valid" && apperror.CodeOf(err) != "agent.tool.record_denied" {
			t.Fatalf("get %s=%v", name, err)
		}
	}
	gateway, run, _, _, _ := interactiveToolGatewayFixture(t)
	gateway.dependencies.Queries = nil
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": AgentToolGetRecord, "object_key": "customer", "record_id": "one"}}}); apperror.CodeOf(err) != "agent.tool.query_unavailable" {
		t.Fatalf("get missing=%v", err)
	}
}

func TestInteractiveToolGatewayProposalBoundaryMatrix(t *testing.T) {
	base := agentsdk.RouteResult{RouteType: agentsdk.AgentRouteProposal, TargetKey: "customer.update", Input: map[string]any{"object_key": "customer"}}
	for name, mutate := range map[string]func(*AgentToolGateway, *agentsdk.RouteResult, *agentToolProposalStub, *agentInteractiveRunRepositoryStub, *string){
		"route": func(_ *AgentToolGateway, r *agentsdk.RouteResult, _ *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, _ *string) {
			r.RouteType = agentsdk.AgentRouteInteractiveQuery
			r.Input["tool"] = AgentToolInvokeAction
		},
		"action": func(_ *AgentToolGateway, r *agentsdk.RouteResult, _ *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, _ *string) {
			r.TargetKey = "other"
		},
		"key": func(_ *AgentToolGateway, _ *agentsdk.RouteResult, _ *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, k *string) {
			*k = ""
		},
		"proposal missing": func(g *AgentToolGateway, _ *agentsdk.RouteResult, _ *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, _ *string) {
			g.dependencies.Proposals = nil
		},
		"object": func(_ *AgentToolGateway, r *agentsdk.RouteResult, _ *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, _ *string) {
			r.Input["object_key"] = "invoice"
		},
		"proposal error": func(_ *AgentToolGateway, _ *agentsdk.RouteResult, p *agentToolProposalStub, _ *agentInteractiveRunRepositoryStub, _ *string) {
			p.err = errors.New("proposal")
		},
	} {
		gateway, run, _, proposal, repository := interactiveToolGatewayFixture(t)
		route := base
		route.Input = map[string]any{"object_key": "customer"}
		key := "idem"
		mutate(gateway, &route, proposal, repository, &key)
		if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: route, IdempotencyKey: key}); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
}

func TestInteractiveToolGatewayTimeoutAndOutputSizeLimits(t *testing.T) {
	for name, configure := range map[string]func(*agentSchemaProviderStub, *agentToolQueryStub){
		"timeout configured": func(s *agentSchemaProviderStub, _ *agentToolQueryStub) {
			s.full.Agents[0].ExecutionLimits.TimeoutSeconds = 1
		},
		"output size": func(s *agentSchemaProviderStub, q *agentToolQueryStub) {
			s.full.Agents[0].ExecutionLimits.MaxOutputBytes = 8
			q.output = map[string]any{"content": "too large"}
		},
	} {
		gateway, run, query, _, _ := interactiveToolGatewayFixture(t)
		schema := gateway.dependencies.Authorization.schema.(*agentSchemaProviderStub)
		configure(schema, query)
		for key, snapshot := range schema.filtered {
			snapshot.Agents[0].ExecutionLimits = schema.full.Agents[0].ExecutionLimits
			schema.filtered[key] = snapshot
		}
		result, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}}})
		if name == "timeout configured" && (err != nil || result.Invocation.Status != "executed") {
			t.Fatalf("timeout result=%#v err=%v", result, err)
		}
		if name == "output size" && apperror.CodeOf(err) != "agent.tool.output_invalid" {
			t.Fatalf("output size=%v", err)
		}
	}
	gateway, run, _, _, _ := interactiveToolGatewayFixture(t)
	schema := gateway.dependencies.Authorization.schema.(*agentSchemaProviderStub)
	schema.full.Agents[0].Tools = append(schema.full.Agents[0].Tools, "custom_tool")
	for key, snapshot := range schema.filtered {
		snapshot.Agents[0].Tools = append(snapshot.Agents[0].Tools, "custom_tool")
		schema.filtered[key] = snapshot
	}
	if _, err := gateway.InvokeInteractive(t.Context(), AgentInteractiveToolInvocationRequest{Run: run, Route: agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": "custom_tool"}}}); apperror.CodeOf(err) != "agent.tool.not_allowed" {
		t.Fatalf("custom interactive tool=%v", err)
	}
}
