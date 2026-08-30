package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentPrincipalDirectoryStub struct {
	principals map[string]principalmodel.Principal
	err        error
}

func (s agentPrincipalDirectoryStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	if s.err != nil {
		return identitysdk.PrincipalResolution{}, s.err
	}
	key := string(request.SubjectID)
	if strings.TrimSpace(request.RoleKey) != "" {
		key += ":" + strings.TrimSpace(request.RoleKey)
	}
	return agentPrincipalResolution(s.principals[key]), nil
}

func agentPrincipalResolution(principal principalmodel.Principal) identitysdk.PrincipalResolution {
	bundle := identitysdk.AccessBundle{}
	if principal.AccessBundle != nil {
		bundle = *principal.AccessBundle
	}
	sdkPrincipal := principal.Principal
	sdkPrincipal.AccessBundle = nil
	return identitysdk.PrincipalResolution{Principal: sdkPrincipal, AccessBundle: bundle}
}

type agentSchemaProviderStub struct {
	full     appschemamodel.ApplicationSchemaSnapshot
	filtered map[string]appschemamodel.ApplicationSchemaSnapshot
}

type agentInternalSchemaProviderStub struct {
	agentSchemaProviderStub
	internal appschemamodel.ApplicationSchemaSnapshot
}

func (s agentInternalSchemaProviderStub) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return s.internal
}

func (s agentSchemaProviderStub) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	if !principal.Known {
		return s.full
	}
	return s.filtered[principal.UserID+":"+principal.RoleKey]
}

func TestFullAgentSchemaUsesInternalSnapshotWithoutChangingAnonymousProjection(t *testing.T) {
	provider := agentInternalSchemaProviderStub{
		agentSchemaProviderStub: agentSchemaProviderStub{full: appschemamodel.ApplicationSchemaSnapshot{}},
		internal:                appschemamodel.ApplicationSchemaSnapshot{AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{Key: "service", Enabled: true}}},
	}
	snapshot := fullAgentSchema(t.Context(), provider)
	if len(snapshot.AgentServicePrincipals) != 1 || snapshot.AgentServicePrincipals[0].Key != "service" {
		t.Fatalf("internal Agent schema was not selected: %#v", snapshot.AgentServicePrincipals)
	}
}

type agentRecordVisibilityStub struct {
	denied map[string]bool
	err    error
}

type sequencedAgentPrincipalDirectory struct {
	base      *agentPrincipalDirectoryStub
	roleCalls int
	second    principalmodel.Principal
	secondErr error
}

func (s *sequencedAgentPrincipalDirectory) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	s.roleCalls++
	if s.roleCalls == 2 {
		return agentPrincipalResolution(s.second), s.secondErr
	}
	return s.base.Resolve(ctx, request)
}

type sequencedAgentSchemaProvider struct {
	base       *agentSchemaProviderStub
	fullCalls  int
	secondFull appschemamodel.ApplicationSchemaSnapshot
}

func (s *sequencedAgentSchemaProvider) SchemaForPrincipal(ctx context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	if !principal.Known {
		s.fullCalls++
		if s.fullCalls == 2 {
			return s.secondFull
		}
	}
	return s.base.SchemaForPrincipal(ctx, principal)
}

func (s agentRecordVisibilityStub) CanReadAgentRecord(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return !s.denied[objectKey+":"+recordID], nil
}

func TestAgentExecutionIdentityRefreshesAndSeparatesInitiator(t *testing.T) {
	service, initiator, _, _ := agentAuthorizationFixture()
	execution, identity, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}})
	if err != nil {
		t.Fatal(err)
	}
	if execution.AuthorizationRevision != "operator-rev-2" || identity.Initiator.AuthorizationRevision != "operator-rev-1" || identity.Execution.AuthorizationRevision != "operator-rev-2" {
		t.Fatalf("identity refresh evidence = %#v execution=%#v", identity, execution)
	}
	if identity.Initiator.UserID != identity.Execution.UserID || identity.Mode != agentsdk.AgentTaskIdentityInherit {
		t.Fatalf("inherit identity changed actor: %#v", identity)
	}

	execution, identity, err = service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}, ExpectedRotationVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	if execution.UserID != "agent_customer" || identity.Initiator.UserID != "operator" || identity.Execution.UserID != "agent_customer" || identity.ServiceRotationVersion != 3 {
		t.Fatalf("service identity boundary = %#v execution=%#v", identity, execution)
	}
}

func TestAgentExecutionIdentityFailsClosedOnRevocationAndRotation(t *testing.T) {
	service, initiator, schema, directory := agentAuthorizationFixture()
	tests := []struct {
		name   string
		mutate func()
		req    AgentExecutionIdentityRequest
		code   string
	}{
		{name: "unknown initiator", mutate: func() {}, req: AgentExecutionIdentityRequest{Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}, code: "agent.authorization.initiator_inactive"},
		{name: "revoked role", mutate: func() {
			directory.principals["operator:operator"] = principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
		}, req: AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}, code: "agent.authorization.execution_principal_inactive"},
		{name: "workspace removed", mutate: func() {
			value := directory.principals["operator:operator"]
			value.WorkspaceID = "other"
			directory.principals["operator:operator"] = value
		}, req: AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}, code: "agent.authorization.workspace_removed"},
		{name: "service disabled", mutate: func() { schema.full.AgentServicePrincipals[0].Enabled = false }, req: AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}}, code: "agent.authorization.service_principal_disabled"},
		{name: "rotation stale", mutate: func() {}, req: AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}, ExpectedRotationVersion: 2}, code: "agent.authorization.service_rotation_stale"},
		{name: "unknown mode", mutate: func() {}, req: AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: "root"}}, code: "agent.authorization.identity_mode_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local, localInitiator, localSchema, localDirectory := agentAuthorizationFixture()
			service, initiator, schema, directory = local, localInitiator, localSchema, localDirectory
			test.mutate()
			request := test.req
			if request.Initiator.UserID == "operator" {
				request.Initiator = initiator
			}
			_, _, err := service.ResolveExecutionIdentity(t.Context(), request)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("error = %v code=%q want %q", err, apperror.CodeOf(err), test.code)
			}
		})
	}
	unavailable := NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{})
	if _, _, err := unavailable.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
		t.Fatalf("missing resolver error = %v", err)
	}
	failure := errors.New("directory offline")
	service.principals = agentPrincipalDirectoryStub{err: failure}
	if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}); !errors.Is(err, failure) {
		t.Fatalf("directory failure = %v", err)
	}
}

func TestAgentTaskAuthorizationIntersectsEveryCapabilityLayer(t *testing.T) {
	service, initiator, schema, _ := agentAuthorizationFixture()
	authorized, err := service.AuthorizeTask(t.Context(), AgentTaskAuthorizationRequest{
		Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: "customer.review", TaskVersion: "1.0.0",
		NodeAllowedObjects: []string{"customer"}, NodeAllowedActions: []string{"customer.update"}, NodeAllowedOutcomes: []string{"success"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(authorized.AllowedObjects, ",") != "customer" || strings.Join(authorized.AllowedActions, ",") != "customer.update" || strings.Join(authorized.AllowedOutcomes, ",") != "success" {
		t.Fatalf("authorization intersection = %#v", authorized)
	}
	if strings.Join(authorized.VisibleFields["customer"], ",") != "name" || authorized.Evidence.PolicyRevision == "" || authorized.Evidence.AuthorizationRevision != "operator-rev-2" {
		t.Fatalf("authorization evidence/fields = %#v", authorized)
	}
	request := AgentTaskAuthorizationRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: "customer.review", TaskVersion: "1.0.0"}
	request.NodeAllowedObjects = []string{"invoice"}
	if _, err := service.AuthorizeTask(t.Context(), request); apperror.CodeOf(err) != "agent.authorization.node_allowlist_widening" {
		t.Fatalf("node widening error = %v", err)
	}
	request.NodeAllowedObjects = nil
	request.TaskVersion = "2.0.0"
	if _, err := service.AuthorizeTask(t.Context(), request); apperror.CodeOf(err) != "agent.authorization.task_unpublished" {
		t.Fatalf("unpublished task error = %v", err)
	}
	request.TaskVersion = "1.0.0"
	schema.filtered["operator:operator"] = appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "revoked", AgentTasks: schema.full.AgentTasks}
	if _, err := service.AuthorizeTask(t.Context(), request); apperror.CodeOf(err) != "agent.authorization.capability_revoked" {
		t.Fatalf("revoked capability error = %v", err)
	}
}

func TestGlobalAgentContextUsesTrustedCurrentRouteAndPrincipal(t *testing.T) {
	service, initiator, _, _ := agentAuthorizationFixture()
	request := GlobalAgentContextRequest{
		Principal: initiator, EntrypointKey: "assistant.global", Surface: "business_workspace", RouteKey: "workspace.customer", ObjectKey: "customer",
		RecordID: "customer-1", SelectedRecordIDs: []string{"customer-2", "customer-2"}, Locale: "zh-CN", Timezone: "Asia/Shanghai",
		AvailableOperationIDs: []string{"task:customer.review", "action:customer.update", "action:invoice.pay", "workflow:missing"},
	}
	resolved, err := service.ResolveGlobalContext(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ContextRevision == "" || resolved.Principal.AuthorizationRevision != "operator-rev-2" || resolved.Principal.RoleKey != "operator" {
		t.Fatalf("context principal/revision = %#v", resolved)
	}
	if strings.Join(resolved.AvailableOperations, ",") != "action:customer.update,task:customer.review" || len(resolved.SelectedRecordIDs) != 1 {
		t.Fatalf("untrusted operation list widened context: %#v", resolved)
	}
	for name, mutate := range map[string]func(*GlobalAgentContextRequest){
		"surface": func(r *GlobalAgentContextRequest) { r.Surface = "admin_console" },
		"route":   func(r *GlobalAgentContextRequest) { r.RouteKey = "admin.root" },
		"object": func(r *GlobalAgentContextRequest) {
			r.ObjectKey = "invoice"
			r.RecordID = ""
			r.SelectedRecordIDs = nil
		},
		"record": func(r *GlobalAgentContextRequest) {
			r.RecordID = "denied"
			service.records = agentRecordVisibilityStub{denied: map[string]bool{"customer:denied": true}}
		},
		"selection": func(r *GlobalAgentContextRequest) {
			r.SelectedRecordIDs = make([]string, 21)
			for i := range r.SelectedRecordIDs {
				r.SelectedRecordIDs[i] = string(rune('a' + i))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			local, _, _, _ := agentAuthorizationFixture()
			candidate := request
			mutate(&candidate)
			if name != "record" {
				service = local
			}
			if _, err := service.ResolveGlobalContext(t.Context(), candidate); err == nil {
				t.Fatal("expected trusted context resolution to fail")
			}
		})
	}
}

func TestAgentExecutionIdentityBoundaryMatrix(t *testing.T) {
	service, initiator, schema, directory := agentAuthorizationFixture()
	for name, candidate := range map[string]principalmodel.Principal{
		"invalid workspace": principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: ""}},
		"unknown":           principalmodel.Principal{Principal: identitysdk.Principal{UserID: "operator", WorkspaceID: "workspace-1"}},
		"empty user":        principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}},
	} {
		if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: candidate, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}); apperror.CodeOf(err) != "agent.authorization.initiator_inactive" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	for name, candidate := range map[string]*AgentAuthorizationApplicationService{
		"nil":        nil,
		"principals": NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Schema: schema}),
		"schema":     NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Principals: directory}),
	} {
		if _, _, err := candidate.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "missing"}}); apperror.CodeOf(err) != "agent.authorization.service_principal_disabled" {
		t.Fatalf("missing service=%v", err)
	}
	if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}}); err != nil {
		t.Fatalf("zero expected rotation=%v", err)
	}
	directory.err = errors.New("service directory")
	if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}}); !errors.Is(err, directory.err) {
		t.Fatalf("service directory=%v", err)
	}
	directory.err = nil
	for name, workspace := range map[string]string{"invalid": "", "mismatch": "other"} {
		value := directory.principals["operator:operator"]
		value.WorkspaceID = workspace
		directory.principals["operator:operator"] = value
		if _, _, err := service.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}}); apperror.CodeOf(err) != "agent.authorization.workspace_removed" {
			t.Fatalf("execution workspace %s=%v", name, err)
		}
		value.WorkspaceID = initiator.WorkspaceID
		directory.principals["operator:operator"] = value
	}
}

func TestAgentTaskAuthorizationBoundaryMatrix(t *testing.T) {
	baseRequest := func(initiator principalmodel.Principal) AgentTaskAuthorizationRequest {
		return AgentTaskAuthorizationRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: "customer.review", TaskVersion: "1.0.0"}
	}
	service, initiator, schema, _ := agentAuthorizationFixture()
	badIdentity := baseRequest(principalmodel.Principal{})
	if _, err := service.AuthorizeTask(t.Context(), badIdentity); apperror.CodeOf(err) != "agent.authorization.initiator_inactive" {
		t.Fatalf("identity=%v", err)
	}
	for name, mutate := range map[string]func(*AgentTaskAuthorizationRequest){
		"object":  func(r *AgentTaskAuthorizationRequest) { r.NodeAllowedObjects = []string{"invoice"} },
		"action":  func(r *AgentTaskAuthorizationRequest) { r.NodeAllowedActions = []string{"invoice.pay"} },
		"outcome": func(r *AgentTaskAuthorizationRequest) { r.NodeAllowedOutcomes = []string{"root"} },
	} {
		request := baseRequest(initiator)
		mutate(&request)
		if _, err := service.AuthorizeTask(t.Context(), request); apperror.CodeOf(err) != "agent.authorization.node_allowlist_widening" {
			t.Fatalf("widen %s=%v", name, err)
		}
	}
	for name, mutate := range map[string]func(*agentSchemaProviderStub){
		"missing":  func(s *agentSchemaProviderStub) { s.full.AgentTasks = nil },
		"disabled": func(s *agentSchemaProviderStub) { s.full.AgentTasks[0].Enabled = false },
	} {
		local, localInitiator, localSchema, _ := agentAuthorizationFixture()
		mutate(localSchema)
		if _, err := local.AuthorizeTask(t.Context(), baseRequest(localInitiator)); apperror.CodeOf(err) != "agent.authorization.task_unpublished" {
			t.Fatalf("task %s=%v", name, err)
		}
	}
	for name, visible := range map[string]appschemamodel.ApplicationSchemaSnapshot{
		"objects": {AgentTasks: schema.full.AgentTasks, Actions: schema.full.Actions, Agents: schema.full.Agents, Skills: schema.full.Skills},
		"actions": {AgentTasks: schema.full.AgentTasks, Objects: schema.full.Objects, Agents: schema.full.Agents, Skills: schema.full.Skills},
	} {
		local, localInitiator, localSchema, _ := agentAuthorizationFixture()
		localSchema.filtered["operator:operator"] = visible
		if _, err := local.AuthorizeTask(t.Context(), baseRequest(localInitiator)); apperror.CodeOf(err) != "agent.authorization.capability_revoked" {
			t.Fatalf("revoked %s=%v", name, err)
		}
	}
	local, localInitiator, localSchema, _ := agentAuthorizationFixture()
	task := localSchema.full.AgentTasks[0]
	task.SideEffectMode = agentsdk.AgentTaskSideEffectAnalysisOnly
	localSchema.full.AgentTasks[0] = task
	visible := localSchema.filtered["operator:operator"]
	visible.AgentTasks[0] = task
	visible.Actions = nil
	localSchema.filtered["operator:operator"] = visible
	if _, err := local.AuthorizeTask(t.Context(), baseRequest(localInitiator)); err != nil {
		t.Fatalf("analysis-only without actions=%v", err)
	}
	if tools := agentToolsForTask(appschemamodel.ApplicationSchemaSnapshot{Agents: []agentsdk.AgentSchema{{Key: "other"}}, Skills: []agentsdk.SkillSchema{{Key: "unused", AllowedTools: []string{"x"}}}}, task); len(tools) != 0 {
		t.Fatalf("unmatched tools=%v", tools)
	}
	local, localInitiator, localSchema, _ = agentAuthorizationFixture()
	emptyTask := localSchema.full.AgentTasks[0]
	emptyTask.AllowedObjects, emptyTask.AllowedActions, emptyTask.SideEffectMode = nil, nil, agentsdk.AgentTaskSideEffectAnalysisOnly
	localSchema.full.AgentTasks[0] = emptyTask
	visible = localSchema.filtered["operator:operator"]
	visible.AgentTasks[0], visible.Objects = emptyTask, append(visible.Objects, definitionmodel.ObjectSchema{Key: "invoice"})
	localSchema.filtered["operator:operator"] = visible
	if _, err := local.AuthorizeTask(t.Context(), baseRequest(localInitiator)); err != nil {
		t.Fatalf("empty capabilities=%v", err)
	}
}

func TestGlobalAgentContextBoundaryMatrix(t *testing.T) {
	base := func(initiator principalmodel.Principal) GlobalAgentContextRequest {
		return GlobalAgentContextRequest{Principal: initiator, EntrypointKey: "assistant.global", Surface: "business_workspace", RouteKey: "workspace.customer", ObjectKey: "customer"}
	}
	for name, test := range map[string]struct {
		mutate func(*AgentAuthorizationApplicationService, *agentSchemaProviderStub, *GlobalAgentContextRequest)
		code   string
	}{
		"identity": {func(_ *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			r.Principal = principalmodel.Principal{}
		}, "agent.authorization.initiator_inactive"},
		"assignment missing": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			s.full.AgentEntrypoints = nil
		}, "agent.authorization.entrypoint_denied"},
		"assignment disabled": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			s.full.AgentEntrypoints[0].Enabled = false
		}, "agent.authorization.entrypoint_denied"},
		"permission": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			s.full.AgentEntrypoints[0].RequiredPermissions = []string{"admin.access"}
		}, "agent.authorization.entrypoint_denied"},
		"route missing": {func(_ *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			r.RouteKey = "admin.missing"
		}, "agent.authorization.route_denied"},
		"pattern": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			s.full.AgentEntrypoints[0].RoutePatterns = []string{"other.*"}
		}, "agent.authorization.route_denied"},
		"visible object": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			v := s.filtered["operator:operator"]
			v.Objects = nil
			s.filtered["operator:operator"] = v
		}, "agent.authorization.object_denied"},
		"record object": {func(_ *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			r.ObjectKey = ""
			r.RecordID = "one"
		}, "agent.authorization.record_denied"},
		"record service": {func(s *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			s.records = nil
			r.RecordID = "one"
		}, "agent.authorization.record_denied"},
		"record error": {func(s *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			s.records = agentRecordVisibilityStub{err: errors.New("visibility")}
			r.RecordID = "one"
		}, ""},
		"record denied": {func(s *AgentAuthorizationApplicationService, _ *agentSchemaProviderStub, r *GlobalAgentContextRequest) {
			s.records = agentRecordVisibilityStub{denied: map[string]bool{"customer:one": true}}
			r.RecordID = "one"
		}, "agent.authorization.record_denied"},
		"context size": {func(_ *AgentAuthorizationApplicationService, s *agentSchemaProviderStub, _ *GlobalAgentContextRequest) {
			s.full.AgentEntrypoints[0].ContextContract.MaxContextBytes = 1
		}, "agent.authorization.context_limit"},
	} {
		t.Run(name, func(t *testing.T) {
			service, initiator, schema, _ := agentAuthorizationFixture()
			request := base(initiator)
			test.mutate(service, schema, &request)
			_, err := service.ResolveGlobalContext(t.Context(), request)
			if err == nil {
				t.Fatal("expected error")
			}
			if test.code != "" && apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
	service, initiator, schema, _ := agentAuthorizationFixture()
	request := base(initiator)
	request.ObjectKey = ""
	request.AvailableOperationIDs = nil
	if result, err := service.ResolveGlobalContext(t.Context(), request); err != nil || !agentContains(result.AvailableOperations, "action:customer.update") {
		t.Fatalf("objectless context=%#v err=%v", result, err)
	}
	visible := schema.filtered["operator:operator"]
	visible.Actions = append(visible.Actions, definitionmodel.ActionSchema{Key: "invoice.pay", ObjectKey: "invoice"})
	schema.filtered["operator:operator"] = visible
	if _, err := service.ResolveGlobalContext(t.Context(), base(initiator)); err != nil {
		t.Fatalf("unrelated action=%v", err)
	}
}

func TestAgentAuthorizationLookupAndCollectionHelpers(t *testing.T) {
	if _, found := findAgentServicePrincipal(appschemamodel.ApplicationSchemaSnapshot{}, "x"); found {
		t.Fatal("service found")
	}
	if _, found := findAgentTask(nil, "x", "1"); found {
		t.Fatal("task found")
	}
	if _, found := findAgentTask([]agentsdk.AgentTaskDefinition{{Key: "other", Version: "1"}, {Key: "x", Version: "2"}}, "x", "1"); found {
		t.Fatal("mismatched task found")
	}
	if _, found := findAgentEntrypoint(nil, "x"); found {
		t.Fatal("entrypoint found")
	}
	if _, found := findAgentEntrypoint([]agentsdk.AgentEntrypointAssignment{{Key: "other"}}, "x"); found {
		t.Fatal("mismatched entrypoint found")
	}
	if !agentRouteAllowed([]string{"exact"}, "exact") || !agentRouteAllowed([]string{"workspace.*"}, "workspace.customer") || agentRouteAllowed([]string{"workspace.*"}, "admin.root") || agentRouteAllowed([]string{"other"}, "missing") {
		t.Fatal("route matching")
	}
	if agentSubset([]string{"x"}, nil) || !agentSubset(nil, nil) {
		t.Fatal("subset")
	}
	if got := agentEffectiveNodeLimit([]string{"a"}, nil); len(got) != 1 || got[0] != "a" {
		t.Fatalf("effective=%v", got)
	}
	if got := agentIntersect([]string{"a", "b"}, map[string]bool{"b": true}); len(got) != 1 || got[0] != "b" {
		t.Fatalf("intersect=%v", got)
	}
	if got := agentUniqueStrings([]string{"", " a ", "a"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("unique=%v", got)
	}
	if len(agentTaskKeys([]agentsdk.AgentTaskDefinition{{Key: "off"}})) != 0 || len(agentWorkflowKeys([]definitionmodel.WorkflowSchema{{Key: "off"}})) != 0 {
		t.Fatal("disabled keys")
	}
	if values := agentStringsFromAny("invalid"); values != nil {
		t.Fatalf("strings=%v", values)
	}
}

func TestAgentInteractiveAuthorizationBoundaryMatrix(t *testing.T) {
	service, initiator, _, directory := agentAuthorizationFixture()
	initiator.SurfaceKey = "business_workspace"
	stored, err := service.ResolveGlobalContext(t.Context(), GlobalAgentContextRequest{Principal: initiator, EntrypointKey: "assistant.global", Surface: initiator.SurfaceKey, RouteKey: "workspace.customer", ObjectKey: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]*AgentAuthorizationApplicationService{"nil": nil, "schema": NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{})} {
		if _, err := candidate.AuthorizeInteractive(t.Context(), stored, initiator); apperror.CodeOf(err) != "agent.authorization.schema_unavailable" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	badPrincipal := initiator
	badPrincipal.Known = false
	if _, err := service.AuthorizeInteractive(t.Context(), stored, badPrincipal); err == nil {
		t.Fatal("expected context resolution error")
	}
	withoutRevision := stored
	withoutRevision.ContextRevision = ""
	if _, err := service.AuthorizeInteractive(t.Context(), withoutRevision, initiator); err != nil {
		t.Fatalf("empty revision=%v", err)
	}
	directory.err = errors.New("principal")
	if _, err := service.AuthorizeInteractive(t.Context(), stored, initiator); !errors.Is(err, directory.err) {
		t.Fatalf("principal error=%v", err)
	}
	directory.err = nil
	for name, workspace := range map[string]string{"unknown": initiator.WorkspaceID, "workspace": "other"} {
		value := directory.principals["operator:operator"]
		value.WorkspaceID = workspace
		if name == "unknown" {
			value.Known = false
		}
		directory.principals["operator:operator"] = value
		want := "agent.authorization.execution_principal_inactive"
		if name == "workspace" {
			want = "agent.authorization.workspace_removed"
		}
		if _, err := service.AuthorizeInteractive(t.Context(), stored, initiator); apperror.CodeOf(err) != want {
			t.Fatalf("live %s=%v", name, err)
		}
		value.Known, value.WorkspaceID = true, initiator.WorkspaceID
		directory.principals["operator:operator"] = value
	}
	for name, test := range map[string]struct {
		mutate func(*agentSchemaProviderStub)
		code   string
	}{
		"entrypoint missing": {func(s *agentSchemaProviderStub) { s.full.AgentEntrypoints = nil }, "agent.authorization.entrypoint_denied"},
		"entrypoint agent":   {func(s *agentSchemaProviderStub) { s.full.AgentEntrypoints[0].AgentKey = "other" }, "agent.authorization.context_stale"},
		"agent missing": {func(s *agentSchemaProviderStub) {
			v := s.filtered["operator:operator"]
			v.Agents = nil
			s.filtered["operator:operator"] = v
		}, "agent.authorization.agent_unpublished"},
	} {
		local, localInitiator, localSchema, _ := agentAuthorizationFixture()
		localInitiator.SurfaceKey = "business_workspace"
		test.mutate(localSchema)
		if _, err := local.AuthorizeInteractive(t.Context(), stored, localInitiator); apperror.CodeOf(err) != test.code {
			t.Fatalf("%s=%v", name, err)
		}
	}
	local, localInitiator, localSchema, _ := agentAuthorizationFixture()
	localInitiator.SurfaceKey = "business_workspace"
	localSchema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal}
	localSchema.full.Workflows = append(localSchema.full.Workflows, definitionmodel.WorkflowSchema{Key: "disabled", Enabled: false}, definitionmodel.WorkflowSchema{Key: "published", Enabled: true, PublishedVersion: 2})
	localSchema.full.AgentEntrypoints[0].AllowedWorkflowKeys = append(localSchema.full.AgentEntrypoints[0].AllowedWorkflowKeys, "published")
	visible := localSchema.filtered["operator:operator"]
	visible.AgentTasks = append(visible.AgentTasks, agentsdk.AgentTaskDefinition{Key: "disabled", Enabled: false}, agentsdk.AgentTaskDefinition{Key: "other", Enabled: true})
	visible.Workflows = append(localSchema.full.Workflows, definitionmodel.WorkflowSchema{Key: "other", Enabled: true})
	visible.Objects = append(visible.Objects, definitionmodel.ObjectSchema{Key: "invoice"})
	visible.AgentEntrypoints = localSchema.full.AgentEntrypoints
	localSchema.filtered["operator:operator"] = visible
	storedWithOps := stored
	storedWithOps.AvailableOperations = []string{"other", "action:customer.update"}
	storedWithOps.ContextRevision = ""
	authorized, err := local.AuthorizeInteractive(t.Context(), storedWithOps, localInitiator)
	if err != nil || len(authorized.Candidates) < 4 {
		t.Fatalf("candidates=%#v err=%v", authorized.Candidates, err)
	}
	if _, found := findInteractiveAgent([]agentsdk.AgentSchema{{Key: "other"}}, "missing"); found {
		t.Fatal("unexpected agent")
	}
	if tools := agentToolsForInteractiveAgent(appschemamodel.ApplicationSchemaSnapshot{Skills: []agentsdk.SkillSchema{{Key: "other", AllowedTools: []string{"x"}}}}, agentsdk.AgentSchema{SkillKeys: []string{"wanted"}}); len(tools) != 0 {
		t.Fatalf("tools=%v", tools)
	}
	localSchema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentsdk.AgentRouteWorkflow}
	visible = localSchema.filtered["operator:operator"]
	visible.Workflows[0].DefinitionVersionID = "version-1"
	localSchema.filtered["operator:operator"] = visible
	storedNoRevision := stored
	storedNoRevision.ContextRevision = ""
	if authorized, err := local.AuthorizeInteractive(t.Context(), storedNoRevision, localInitiator); err != nil || len(authorized.Candidates) != 2 || authorized.Candidates[0].Version != "version-1" || authorized.Candidates[1].Version != "2" {
		t.Fatalf("definition version candidates=%#v err=%v", authorized.Candidates, err)
	}
}

func TestAgentInteractiveAuthorizationDetectsMidRequestIdentityAndSchemaChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		second principalmodel.Principal
		err    error
		code   string
	}{
		{"directory error", principalmodel.Principal{}, errors.New("second lookup"), ""},
		{"inactive", principalmodel.Principal{}, nil, "agent.authorization.execution_principal_inactive"},
		{"workspace", accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "other"}}, accessfixture.Bundle{Key: "operator"}), nil, "agent.authorization.execution_principal_inactive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, initiator, schema, directory := agentAuthorizationFixture()
			initiator.SurfaceKey = "business_workspace"
			sequence := &sequencedAgentPrincipalDirectory{base: directory, second: test.second, secondErr: test.err}
			service := NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Principals: sequence, Schema: schema, Records: agentRecordVisibilityStub{denied: map[string]bool{}}})
			stored := agentsdk.GlobalContext{EntrypointKey: "assistant.global", AgentKey: "customer_agent", Surface: "business_workspace", RouteKey: "workspace.customer", ObjectKey: "customer"}
			_, err := service.AuthorizeInteractive(t.Context(), stored, initiator)
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("err=%v", err)
			}
			if test.code != "" && apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
	for name, mutate := range map[string]func(*appschemamodel.ApplicationSchemaSnapshot){
		"missing":  func(full *appschemamodel.ApplicationSchemaSnapshot) { full.AgentEntrypoints = nil },
		"mismatch": func(full *appschemamodel.ApplicationSchemaSnapshot) { full.AgentEntrypoints[0].AgentKey = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			_, initiator, schema, directory := agentAuthorizationFixture()
			initiator.SurfaceKey = "business_workspace"
			second := schema.full
			second.AgentEntrypoints = append([]agentsdk.AgentEntrypointAssignment(nil), schema.full.AgentEntrypoints...)
			mutate(&second)
			service := NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Principals: directory, Schema: &sequencedAgentSchemaProvider{base: schema, secondFull: second}, Records: agentRecordVisibilityStub{denied: map[string]bool{}}})
			stored := agentsdk.GlobalContext{EntrypointKey: "assistant.global", AgentKey: "customer_agent", Surface: "business_workspace", RouteKey: "workspace.customer", ObjectKey: "customer"}
			if _, err := service.AuthorizeInteractive(t.Context(), stored, initiator); apperror.CodeOf(err) != "agent.authorization.entrypoint_denied" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func agentAuthorizationFixture() (*AgentAuthorizationApplicationService, principalmodel.Principal, *agentSchemaProviderStub, *agentPrincipalDirectoryStub) {
	operatorRole := accessfixture.Bundle{Key: "operator", Permissions: []string{"customer.read", "customer.update"}, RecordScope: "own_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "own_records", Read: true, Write: true}}}
	serviceRole := accessfixture.Bundle{Key: "agent_service", Permissions: []string{"customer.read"}, RecordScope: "own_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "own_records", Read: true}}}
	initiator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace-1", AuthorizationRevision: "operator-rev-1"}, RequestID: "request-1"}, operatorRole)
	initiator.AuthorizationRevision = "operator-rev-1"
	fresh := initiator
	fresh.AuthorizationRevision = "operator-rev-2"
	servicePrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "agent_customer", WorkspaceID: "workspace-1", AuthorizationRevision: "service-rev-2"}}, serviceRole)
	directory := &agentPrincipalDirectoryStub{principals: map[string]principalmodel.Principal{"operator:operator": fresh, "agent_customer:agent_service": servicePrincipal}}
	task := agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "customer.review", Version: "1.0.0", AgentKey: "customer_agent", AllowedObjects: []string{"customer"}, AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success", "manual_review", "error"}, SideEffectMode: agentsdk.AgentTaskSideEffectActionAllowed, Enabled: true}
	entrypoint := agentsdk.AgentEntrypointAssignment{ContractVersion: agentsdk.AgentEntrypointContractVersion, Key: "assistant.global", AgentKey: "customer_agent", Surface: "business_workspace", RequiredPermissions: []string{"customer.read"}, RoutePatterns: []string{"workspace.*"}, AllowedTaskKeys: []string{"customer.review"}, AllowedWorkflowKeys: []string{"customer.flow"}, Enabled: true, ContextContract: agentsdk.GlobalAgentContextContract{ContractVersion: agentsdk.GlobalAgentContextContractVersion, MaxSelectedRecord: 20, MaxContextBytes: 65536}, RoutingContract: agentsdk.AgentRoutingContract{ContractVersion: agentsdk.AgentRoutingContractVersion, AllowedRouteTypes: []string{agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow}}}
	full := appschemamodel.ApplicationSchemaSnapshot{
		SchemaHash: "schema-full", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "secret"}}}, {Key: "invoice"}},
		Actions:   []definitionmodel.ActionSchema{{Key: "customer.update", ObjectKey: "customer"}, {Key: "invoice.pay", ObjectKey: "invoice"}},
		Workflows: []definitionmodel.WorkflowSchema{{Key: "customer.flow", Enabled: true}},
		Skills:    []agentsdk.SkillSchema{{Key: "customer_reader", Version: "1.0.0", Name: "Reader", AllowedObjects: []string{"customer"}, AllowedTools: []string{"query_records"}}}, Agents: []agentsdk.AgentSchema{{Key: "customer_agent", Version: "1.0.0", Name: "Agent", SkillKeys: []string{"customer_reader"}, Tools: []string{"invoke_action"}}},
		AgentTasks: []agentsdk.AgentTaskDefinition{task}, AgentEntrypoints: []agentsdk.AgentEntrypointAssignment{entrypoint}, AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{ContractVersion: agentsdk.AgentServicePrincipalContractVersion, Key: "customer_service", UserID: "agent_customer", RoleKey: "agent_service", Enabled: true, RotationVersion: 3}},
	}
	filtered := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "schema-operator", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, Actions: []definitionmodel.ActionSchema{{Key: "customer.update", ObjectKey: "customer"}}, Workflows: full.Workflows, Skills: full.Skills, Agents: full.Agents, AgentTasks: full.AgentTasks, AgentEntrypoints: full.AgentEntrypoints}
	schema := &agentSchemaProviderStub{full: full, filtered: map[string]appschemamodel.ApplicationSchemaSnapshot{"operator:operator": filtered, "agent_customer:agent_service": filtered}}
	service := NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Principals: directory, Schema: schema, Records: agentRecordVisibilityStub{denied: map[string]bool{}}})
	return service, initiator, schema, directory
}
