package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/ratelimit"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

type agentRecordVisibilityAdapter struct {
	records *recordapplication.RecordApplicationService
}

func (a agentRecordVisibilityAdapter) CanReadAgentRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (bool, error) {
	if a.records == nil {
		return false, nil
	}
	return a.records.RecordScopeAllows(ctx, objectKey, recordID, principal)
}

// AgentApplicationHostDependencies contains the host-owned application ports
// needed to finish Agent's product Adapter before authorization reconciliation.
// HTTP mounting remains a later transport step.
type AgentApplicationHostDependencies struct {
	RuntimeID                 string
	Application               identitysdk.ApplicationScope
	Binding                   agentsdk.Binding
	Integration               integrationsdk.Binding
	Records                   *composition.RuntimeServices
	Principals                identitysdk.PrincipalResolver
	RateLimiter               ratelimit.Limiter
	IntegrationSecretKey      string
	IdentityIssuer            string
	NotificationEvents        func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
	ConversationCodeRuntime   agentsdk.ConversationCodeRuntime
	ConversationCodingRuntime agentsdk.ConversationCodingRuntime
}

// BindAgentApplicationHost closes Agent's application boundary before Runtime
// validates and reconciles the module's complete source-owned Action manifest.
// It is idempotent so HTTP assembly can safely verify an already-bound module.
func BindAgentApplicationHost(dependencies AgentApplicationHostDependencies) error {
	binder, ok := dependencies.Binding.(agentmodulehost.ApplicationHostBinder)
	if !ok || binder == nil {
		return nil
	}
	if complete, err := agentAuthorizationProjectionComplete(dependencies.Binding); err != nil {
		return err
	} else if complete {
		return nil
	}
	if dependencies.Records == nil || dependencies.Principals == nil {
		return fmt.Errorf("Agent application host dependencies are incomplete")
	}
	applications := dependencies.Records.Applications()
	if applications.AgentAuthorization == nil || applications.Records == nil || applications.Workflows == nil || applications.Audit == nil || applications.Schema == nil || applications.Actions == nil {
		return fmt.Errorf("Runtime Agent application ports are incomplete")
	}
	credentialKey := sha256.Sum256([]byte("domainry-agent-task-credential-v1:" + dependencies.IntegrationSecretKey))
	credentials := agentapplication.NewAgentTaskCredentialApplicationService(credentialKey[:], nil, nil)
	tools := agentapplication.NewAgentToolGateway(agentapplication.AgentToolGatewayDependencies{
		Authorization: applications.AgentAuthorization, Credentials: credentials,
		Queries: agentTaskToolQueryAdapter{records: applications.Records}, Actions: agentTaskToolActionAdapter{actions: applications.Actions},
		Risk: agentTaskToolRiskAdapter{actions: applications.Actions}, RateLimiter: dependencies.RateLimiter,
	})
	interactive := runtimeAgentInteractiveHost{
		authorization: applications.AgentAuthorization, tools: tools, workflows: applications.Workflows,
		records: applications.Records, attachmentFiles: dependencies.Records.AgentTaskAttachmentFiles(),
	}
	task := runtimeAgentTaskHost{authorization: applications.AgentAuthorization, credentials: credentials, tools: tools, workflows: applications.Workflows}
	proposal := runtimeAgentProposalHost{records: dependencies.Records, principals: dependencies.Principals}
	audit := runtimeAgentAuditHost{audit: applications.Audit}
	analysis := runtimeAgentAnalysisHost{catalog: applications.Schema, records: applications.Records}
	conversations, err := NewConversationBusinessSource(ConversationBusinessDependencies{RuntimeID: dependencies.RuntimeID, Application: dependencies.Application, Records: dependencies.Records, Principals: dependencies.Principals, IntegrationSecretKey: dependencies.IntegrationSecretKey, IdentityIssuer: dependencies.IdentityIssuer})
	if err != nil {
		return err
	}
	var accounts integrationsdk.ConnectionAccounts
	var accountReads integrationsdk.ConnectionAccountReads
	var accountWrites integrationsdk.ConnectionAccountWrites
	if dependencies.Integration != nil {
		accountBinding, accountsOK := dependencies.Integration.(integrationsdk.ConnectionAccountsBinding)
		readBinding, readsOK := dependencies.Integration.(integrationsdk.ConnectionAccountReadsBinding)
		writeBinding, writesOK := dependencies.Integration.(integrationsdk.ConnectionAccountWritesBinding)
		if !accountsOK || !readsOK || !writesOK {
			return fmt.Errorf("Integration Binding does not expose MCP account execution ports")
		}
		accounts, accountReads, accountWrites = accountBinding.ConnectionAccounts(), readBinding.ConnectionAccountReads(), writeBinding.ConnectionAccountWrites()
		if accounts == nil || accountReads == nil || accountWrites == nil {
			return fmt.Errorf("Integration Binding returned incomplete MCP account execution ports")
		}
	}
	subject := func(ctx context.Context, authority agentsdk.ConversationAuthority, action string) (integrationsdk.ConnectionAccountSubject, error) {
		principal, err := conversations.ResolveConversationIdentityPrincipal(ctx, authority)
		if err != nil {
			return integrationsdk.ConnectionAccountSubject{}, err
		}
		return connectionAccountSubjectForPrincipal(principal, action)
	}
	followUps := agentFollowUpNotificationPublisher{runtimeID: dependencies.RuntimeID, application: dependencies.Application, principals: dependencies.Principals, publish: dependencies.NotificationEvents}
	if err := binder.BindApplicationHost(runtimeAgentApplicationHost{interactive: interactive, task: task, proposal: proposal, audit: audit, analysis: analysis, conversations: conversations, followUps: followUps, accounts: accounts, accountReads: accountReads, accountWrites: accountWrites, accountSubject: subject, codeRuntime: dependencies.ConversationCodeRuntime, codingRuntime: dependencies.ConversationCodingRuntime}); err != nil {
		return fmt.Errorf("bind Agent application host: %w", err)
	}
	if err := validateAgentAuthorizationProjection(dependencies.Binding); err != nil {
		return fmt.Errorf("validate bound Agent authorization contract: %w", err)
	}
	return nil
}

func connectionAccountSubjectForPrincipal(principal identitysdk.Principal, permissionKey string) (integrationsdk.ConnectionAccountSubject, error) {
	permissionKey = strings.TrimSpace(permissionKey)
	separator := strings.LastIndexByte(permissionKey, '.')
	if separator <= 0 || separator == len(permissionKey)-1 || !principal.Known || principal.AccessBundle == nil {
		return integrationsdk.ConnectionAccountSubject{}, fmt.Errorf("Integration account subject is unavailable")
	}
	workspaceID, userID := strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(principal.UserID)
	if workspaceID == "" || userID == "" || strings.TrimSpace(string(principal.AccessBundle.Subject.WorkspaceID)) != workspaceID || strings.TrimSpace(string(principal.AccessBundle.Subject.SubjectID)) != userID {
		return integrationsdk.ConnectionAccountSubject{}, fmt.Errorf("Integration account subject does not match the principal")
	}
	resource, action := permissionKey[:separator], permissionKey[separator+1:]
	access := integrationsdk.ConnectionAccountAccess{}
	for _, shared := range []bool{false, true} {
		ownerUserID := userID
		if shared {
			ownerUserID = ""
		}
		decision, err := identityevaluator.Evaluate(*principal.AccessBundle, identitysdk.AccessRequest{ObjectKey: resource, Action: action}, identitysdk.ResourceFacts{"workspace_id": workspaceID, "owner_user_id": ownerUserID}, time.Now().UTC())
		if err != nil {
			return integrationsdk.ConnectionAccountSubject{}, err
		}
		if shared {
			access.Workspace = decision.Allowed
		} else {
			access.Personal = decision.Allowed
		}
	}
	if !access.Personal && !access.Workspace {
		return integrationsdk.ConnectionAccountSubject{}, fmt.Errorf("Integration connection account scope is denied")
	}
	return integrationsdk.ConnectionAccountSubject{WorkspaceID: workspaceID, UserID: userID, Access: access}, nil
}

func agentAuthorizationProjectionComplete(binding agentsdk.Binding) (bool, error) {
	provider, hasAdapters := binding.(modulehttp.Provider)
	actions, hasActions := binding.(actioncontract.Provider)
	if !hasAdapters || !hasActions || provider == nil || actions == nil {
		return false, nil
	}
	definitions, err := actions.AuthorizationActions()
	if err != nil {
		return false, fmt.Errorf("load Agent authorization manifest: %w", err)
	}
	return modulehttp.ValidateAuthorizationProjection(definitions, provider) == nil, nil
}

func validateAgentAuthorizationProjection(binding agentsdk.Binding) error {
	provider, hasAdapters := binding.(modulehttp.Provider)
	actions, hasActions := binding.(actioncontract.Provider)
	if !hasAdapters || !hasActions || provider == nil || actions == nil {
		return fmt.Errorf("Agent Binding must provide both HTTP Adapters and its complete Action manifest")
	}
	definitions, err := actions.AuthorizationActions()
	if err != nil {
		return fmt.Errorf("load Agent authorization manifest: %w", err)
	}
	return modulehttp.ValidateAuthorizationProjection(definitions, provider)
}

func (a *httpServerAssembly) bindAgentApplicationHost() {
	if a.dependencies.AgentBinding == nil {
		return
	}
	if err := BindAgentApplicationHost(AgentApplicationHostDependencies{
		RuntimeID:   a.dependencies.RuntimeInstanceID,
		Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(a.dependencies.Config.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(a.dependencies.Config.IdentityAudience)},
		Binding:     a.dependencies.AgentBinding, Integration: a.dependencies.IntegrationBinding, Records: a.dependencies.Records, Principals: a.principals,
		RateLimiter: a.dependencies.RateLimiter, IntegrationSecretKey: a.dependencies.Config.IntegrationSecretKey,
		IdentityIssuer:     a.dependencies.IdentityBinding.Descriptor().Issuer,
		NotificationEvents: a.dependencies.Records.NotificationEventPublisher(),
	}); err != nil {
		panic(err.Error())
	}
	provider, ok := a.dependencies.AgentBinding.(modulehttp.Provider)
	if !ok {
		panic("Agent Binding accepted application host but returned no HTTP adapters")
	}
	adapters := make([]modulehttp.Adapter, 0, len(a.dependencies.ModuleHTTPAdapters)+1)
	for _, adapter := range a.dependencies.ModuleHTTPAdapters {
		if adapter != nil && adapter.Owner() != "agent" {
			adapters = append(adapters, adapter)
		}
	}
	adapters = append(adapters, provider.HTTPAdapters()...)
	a.dependencies.ModuleHTTPAdapters = adapters
}

type runtimeAgentApplicationHost struct {
	codeRuntime    agentsdk.ConversationCodeRuntime
	codingRuntime  agentsdk.ConversationCodingRuntime
	conversations  *agentapplication.ConversationBusinessHost
	interactive    agentmodulehost.InteractiveHost
	task           agentmodulehost.TaskHost
	proposal       agentmodulehost.ProposalHost
	audit          agentmodulehost.AuditHost
	analysis       agentmodulehost.AnalysisHost
	followUps      agentsdk.ConversationFollowUpPublisher
	accounts       integrationsdk.ConnectionAccounts
	accountReads   integrationsdk.ConnectionAccountReads
	accountWrites  integrationsdk.ConnectionAccountWrites
	accountSubject func(context.Context, agentsdk.ConversationAuthority, string) (integrationsdk.ConnectionAccountSubject, error)
}

func (h runtimeAgentApplicationHost) ConversationCodeRuntime() agentsdk.ConversationCodeRuntime {
	return h.codeRuntime
}

func (h runtimeAgentApplicationHost) ConversationCodingRuntime() agentsdk.ConversationCodingRuntime {
	return h.codingRuntime
}

func (h runtimeAgentApplicationHost) ConversationAuthorizer() agentsdk.ConversationToolAuthorizer {
	return h.conversations
}
func (h runtimeAgentApplicationHost) ConversationBusinessSource() agentsdk.ConversationBusinessSource {
	return h.conversations
}
func (h runtimeAgentApplicationHost) ConversationFollowUpPublisher() agentsdk.ConversationFollowUpPublisher {
	return h.followUps
}

func (h runtimeAgentApplicationHost) InteractiveAgent() agentmodulehost.InteractiveHost {
	return h.interactive
}
func (h runtimeAgentApplicationHost) TaskAgent() agentmodulehost.TaskHost { return h.task }
func (h runtimeAgentApplicationHost) ProposalAgent() agentmodulehost.ProposalHost {
	return h.proposal
}
func (h runtimeAgentApplicationHost) AuditAgent() agentmodulehost.AuditHost { return h.audit }
func (h runtimeAgentApplicationHost) AnalysisAgent() agentmodulehost.AnalysisHost {
	return h.analysis
}

type runtimeAgentTaskHost struct {
	authorization *agentapplication.AgentAuthorizationApplicationService
	credentials   *agentapplication.AgentTaskCredentialApplicationService
	tools         *agentapplication.AgentToolGateway
	workflows     *workflowapplication.WorkflowApplicationService
}

func (h runtimeAgentTaskHost) AuthorizeTask(ctx context.Context, request agentmodulehost.TaskAuthorizationRequest) (agentmodulehost.TaskAuthorization, error) {
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: request.Identity.Initiator.WorkspaceID, UserID: request.Identity.Initiator.UserID,
		RoleKey: request.Identity.Initiator.RoleKey, AuthorizationRevision: request.Identity.Initiator.AuthorizationRevision,
	}, CorrelationID: request.CorrelationID}
	objects, actions, outcomes := []string(nil), []string(nil), []string(nil)
	if count := len(request.PreviousEvidence); count > 0 {
		previous := request.PreviousEvidence[count-1]
		objects, actions, outcomes = previous.AllowedObjects, previous.AllowedActions, previous.AllowedOutcomes
	}
	result, err := h.authorization.AuthorizeTask(ctx, agentapplication.AgentTaskAuthorizationRequest{
		Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: request.Identity.Mode, PrincipalKey: request.Identity.ServicePrincipalKey},
		ExpectedRotationVersion: request.Identity.ServiceRotationVersion, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
		NodeAllowedObjects: objects, NodeAllowedActions: actions, NodeAllowedOutcomes: outcomes,
	})
	if err != nil {
		return agentmodulehost.TaskAuthorization{}, err
	}
	return agentmodulehost.TaskAuthorization{
		Principal: runtimeAgentHostPrincipal(result.Principal), Identity: result.Identity, Task: result.Task,
		Evidence: result.Evidence, AllowedTools: append([]string(nil), result.AllowedTools...),
	}, nil
}

func (h runtimeAgentTaskHost) IssueTaskCredential(ctx context.Context, request agentmodulehost.TaskCredentialRequest) (string, error) {
	if h.credentials == nil {
		return "", apperror.New(apperror.KindUnavailable, "agent.task.credential_unavailable", nil, nil)
	}
	return h.credentials.Issue(ctx, agentapplication.AgentTaskCredentialClaims{
		WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskRunID: request.TaskRunID,
		Principal: request.Principal.Reference(), AllowedTools: append([]string(nil), request.AllowedTools...),
	}, time.Duration(request.TTLSeconds)*time.Second)
}

func (h runtimeAgentTaskHost) InvokeTaskTool(ctx context.Context, request agentmodulehost.TaskToolRequest) (agentmodulehost.TaskToolResult, error) {
	if h.tools == nil {
		return agentmodulehost.TaskToolResult{}, apperror.New(apperror.KindUnavailable, "agent.tool.gateway_unavailable", nil, nil)
	}
	objects, actions, outcomes := []string(nil), []string(nil), []string(nil)
	if count := len(request.PreviousEvidence); count > 0 {
		previous := request.PreviousEvidence[count-1]
		objects, actions, outcomes = previous.AllowedObjects, previous.AllowedActions, previous.AllowedOutcomes
	}
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: request.Identity.Initiator.WorkspaceID, UserID: request.Identity.Initiator.UserID,
		RoleKey: request.Identity.Initiator.RoleKey, AuthorizationRevision: request.Identity.Initiator.AuthorizationRevision,
	}}
	result, err := h.tools.Invoke(ctx, agentapplication.AgentToolInvocationRequest{
		Credential: request.Credential, WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskRunID: request.TaskRunID,
		Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: request.Identity.Mode, PrincipalKey: request.Identity.ServicePrincipalKey},
		ExpectedRotationVersion: request.Identity.ServiceRotationVersion, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
		NodeAllowedObjects: objects, NodeAllowedActions: actions, NodeAllowedOutcomes: outcomes,
		Tool: request.Tool, Input: request.Input, IdempotencyKey: request.IdempotencyKey,
	})
	return agentmodulehost.TaskToolResult{
		Status: result.Status, Tool: result.Tool, Output: result.Output, Proposal: result.Proposal, Authorization: result.Authorization,
	}, err
}

func (h runtimeAgentTaskHost) CompleteWorkflowTask(ctx context.Context, request agentmodulehost.WorkflowTaskCompletion) error {
	if h.workflows == nil {
		return apperror.New(apperror.KindUnavailable, "agent.task.workflow_unavailable", nil, nil)
	}
	return h.workflows.CompleteAgentTask(ctx, workflowapplication.WorkflowAgentTaskCompletion{
		WorkspaceID: request.WorkspaceID, TaskRunID: request.TaskRunID, ProcessID: request.ProcessID, NodeInstanceID: request.NodeInstanceID,
		TaskKey: request.TaskKey, TaskVersion: request.TaskVersion, Identity: request.Identity, Status: request.Status,
		Outcome: request.Outcome, Output: request.Output, ErrorCode: request.ErrorCode, Evidence: request.Evidence,
	})
}

type runtimeAgentProposalHost struct {
	records    *composition.RuntimeServices
	principals identitysdk.PrincipalResolver
}

func (h runtimeAgentProposalHost) GuardedWrites(ctx context.Context, principal agentmodulehost.Principal) []agentmodulehost.GuardedWriteContract {
	if h.records == nil {
		return nil
	}
	contracts := h.records.SchemaForPrincipal(ctx, runtimeAgentPrincipal(principal)).GuardedWrites
	result := make([]agentmodulehost.GuardedWriteContract, 0, len(contracts))
	for _, contract := range contracts {
		result = append(result, agentmodulehost.GuardedWriteContract{
			ObjectKey: contract.ObjectKey, Operation: contract.Operation, ActionKey: contract.ActionKey,
			Endpoint: contract.Endpoint, RequiresRecord: contract.RequiresRecord,
		})
	}
	return result
}

func (h runtimeAgentProposalHost) ResolveProposalPrincipal(ctx context.Context, userID, roleKey string) (agentmodulehost.Principal, error) {
	principal, err := resolveRuntimeAgentPrincipalRole(ctx, h.principals, userID, roleKey)
	return runtimeAgentHostPrincipal(principal), err
}

func resolveRuntimeAgentPrincipalRole(ctx context.Context, principals identitysdk.PrincipalResolver, userID, roleKey string) (principalmodel.Principal, error) {
	if principals == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "agent.authorization.resolver_unavailable", nil, nil)
	}
	resolution, err := principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID), RoleKey: roleKey})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, ""), nil
}

func (h runtimeAgentProposalHost) InvokeProposalAction(ctx context.Context, request agentmodulehost.ProposalActionRequest) (agentmodulehost.ProposalActionResult, error) {
	if h.records == nil || h.records.Applications().Actions == nil {
		return agentmodulehost.ProposalActionResult{}, apperror.New(apperror.KindUnavailable, "agent.proposal.action_unavailable", nil, nil)
	}
	result, err := h.records.Applications().Actions.Invoke(ctx, actionmodel.ActionSourceAgent, actionmodel.ActionInvocation{
		ActionKey: request.ActionKey, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Input: request.Input,
		Principal: runtimeAgentPrincipal(request.Principal), Actor: runtimeAgentPrincipal(request.Principal),
		RequestID: request.Principal.RequestID, IdempotencyKey: request.IdempotencyKey,
	})
	return agentmodulehost.ProposalActionResult{Record: result.Record, Object: result.Object}, err
}

func (h runtimeAgentProposalHost) RunProposalWorkflow(ctx context.Context, request agentmodulehost.ProposalWorkflowRequest) (any, error) {
	if h.records == nil || h.records.Applications().Workflows == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.proposal.workflow_unavailable", nil, nil)
	}
	return h.records.Applications().Workflows.RunAgentWorkflow(ctx, request.WorkflowKey, request.Payload, runtimeAgentPrincipal(request.Principal))
}

type runtimeAgentAuditHost struct {
	audit *auditapplication.AuditApplicationService
}

func (h runtimeAgentAuditHost) AppendAgentAudit(ctx context.Context, request agentmodulehost.AuditRequest) error {
	if h.audit == nil {
		return apperror.New(apperror.KindUnavailable, "agent.audit.unavailable", nil, nil)
	}
	return h.audit.AppendAudit(ctx, auditapplication.AuditAppendRequest{
		Family: auditmodel.EventFamilyRuntimeAgent, Event: request.Event, ObjectKey: request.ObjectKey, RecordID: request.RecordID,
		Principal: runtimeAgentPrincipal(request.Principal), Summary: request.Summary,
		Before: request.Before, After: request.After, Metadata: request.Metadata,
	})
}

func (h runtimeAgentAuditHost) ListAgentAudit(ctx context.Context, principal agentmodulehost.Principal, limit int) ([]agentmodulehost.AuditEvent, error) {
	if h.audit == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.audit.unavailable", nil, nil)
	}
	values, err := h.audit.Events(ctx, auditmodel.AuditEventQuery{Limit: limit}, runtimeAgentPrincipal(principal))
	if err != nil {
		return nil, err
	}
	result := make([]agentmodulehost.AuditEvent, 0, len(values))
	for _, value := range values {
		result = append(result, agentmodulehost.AuditEvent{
			Event: value.Event, ObjectKey: value.ObjectKey, RecordID: value.RecordID, Summary: value.Summary,
			Metadata: value.Metadata, CreatedAt: value.CreatedAt,
		})
	}
	return result, nil
}

type runtimeAgentAnalysisHost struct {
	catalog *appschemaapplication.ApplicationSchemaQueryApplicationService
	records *recordapplication.RecordApplicationService
}

func (h runtimeAgentAnalysisHost) ResolveAnalysisCatalog(ctx context.Context, principal agentmodulehost.Principal) (agentmodulehost.AnalysisCatalog, error) {
	if h.catalog == nil {
		return agentmodulehost.AnalysisCatalog{}, apperror.New(apperror.KindUnavailable, "agent.analysis.catalog_unavailable", nil, nil)
	}
	runtimePrincipal := runtimeAgentPrincipal(principal)
	snapshot := h.catalog.ForPrincipal(ctx, runtimePrincipal)
	permissions, err := h.catalog.FeaturePermissions(ctx, runtimePrincipal)
	if err != nil {
		return agentmodulehost.AnalysisCatalog{}, err
	}
	result := agentmodulehost.AnalysisCatalog{MaskedFields: map[string][]string{}}
	for _, object := range snapshot.Objects {
		result.ObjectKeys = append(result.ObjectKeys, object.Key)
	}
	for _, report := range snapshot.Reports {
		result.ReportKeys = append(result.ReportKeys, report.Key)
	}
	for _, field := range permissions.Fields {
		if field.Masked {
			result.MaskedFields[field.ObjectKey] = append(result.MaskedFields[field.ObjectKey], field.FieldKey)
		}
	}
	return result, nil
}

func (h runtimeAgentAnalysisHost) ListAnalysisRecords(ctx context.Context, objectKey string, filters map[string]any, limit int, principal agentmodulehost.Principal) (agentmodulehost.AnalysisRecordPage, error) {
	if h.records == nil {
		return agentmodulehost.AnalysisRecordPage{}, apperror.New(apperror.KindUnavailable, "agent.analysis.records_unavailable", nil, nil)
	}
	page, err := h.records.ListRecords(ctx, objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: limit, Filters: filters}, runtimeAgentPrincipal(principal))
	if err != nil {
		return agentmodulehost.AnalysisRecordPage{}, err
	}
	result := agentmodulehost.AnalysisRecordPage{Total: page.Total, HasNext: page.HasNext, Items: make([]agentmodulehost.AnalysisRecord, 0, len(page.Items))}
	for _, item := range page.Items {
		result.Items = append(result.Items, agentmodulehost.AnalysisRecord{ID: item.ID, Data: item.Data})
	}
	return result, nil
}

type runtimeAgentInteractiveHost struct {
	authorization   *agentapplication.AgentAuthorizationApplicationService
	tools           *agentapplication.AgentToolGateway
	workflows       *workflowapplication.WorkflowApplicationService
	records         *recordapplication.RecordApplicationService
	attachmentFiles *uploadapplication.AgentTaskAttachmentFileService
}

func (h runtimeAgentInteractiveHost) ResolveInteractiveContext(ctx context.Context, request agentmodulehost.InteractiveContextRequest) (agentsdk.GlobalContext, error) {
	return h.authorization.ResolveGlobalContext(ctx, agentapplication.GlobalAgentContextRequest{
		Principal: runtimeAgentPrincipal(request.Principal), EntrypointKey: request.EntrypointKey, RouteKey: request.RouteKey,
		ObjectKey: request.ObjectKey, RecordID: request.RecordID, SelectedRecordIDs: append([]string(nil), request.SelectedRecordIDs...),
		Locale: request.Locale, Timezone: request.Timezone, AvailableOperationIDs: append([]string(nil), request.AvailableOperationIDs...),
	})
}

func (h runtimeAgentInteractiveHost) AuthorizeInteractive(ctx context.Context, request agentmodulehost.InteractiveAuthorizationRequest) (agentmodulehost.InteractiveAuthorization, error) {
	result, err := h.authorization.AuthorizeInteractive(ctx, request.Context, runtimeAgentPrincipal(request.Principal))
	if err != nil {
		return agentmodulehost.InteractiveAuthorization{}, err
	}
	return agentmodulehost.InteractiveAuthorization{
		Principal: runtimeAgentHostPrincipal(result.Principal), Context: result.Context, Agent: result.Agent,
		Candidates: append([]agentsdk.RouteCandidate(nil), result.Candidates...), AllowedTools: append([]string(nil), result.AllowedTools...),
	}, nil
}

func (h runtimeAgentInteractiveHost) AuthorizeInteractiveTask(ctx context.Context, request agentmodulehost.InteractiveTaskAuthorizationRequest) (agentmodulehost.InteractiveTaskAuthorization, error) {
	result, err := h.authorization.AuthorizeTask(ctx, agentapplication.AgentTaskAuthorizationRequest{
		Initiator: runtimeAgentPrincipal(request.Principal), Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit},
		TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
	})
	if err != nil {
		return agentmodulehost.InteractiveTaskAuthorization{}, err
	}
	return agentmodulehost.InteractiveTaskAuthorization{Identity: result.Identity, Task: result.Task, Evidence: result.Evidence}, nil
}

func (h runtimeAgentInteractiveHost) ResolveTaskAttachmentSource(ctx context.Context, request agentmodulehost.TaskAttachmentSourceRequest) (agentsdk.TaskAttachment, error) {
	source := request.Source
	if h.authorization == nil || h.records == nil || h.attachmentFiles == nil {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(apperror.New(apperror.KindUnavailable, "agent.task.attachment_source_unavailable", nil, nil))
	}
	if source.Kind != agentmodulehost.TaskAttachmentSourceKindRuntimeRecordFile {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(apperror.New(apperror.KindBadRequest, "agent.task.attachment_source_invalid", nil, nil))
	}
	principal, _, err := h.authorization.ResolveExecutionIdentity(ctx, agentapplication.AgentExecutionIdentityRequest{
		Initiator: runtimeAgentPrincipal(request.Principal), Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit},
	})
	if err != nil {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(err)
	}
	objectKey, recordID, fieldKey := strings.TrimSpace(source.ObjectKey), strings.TrimSpace(source.RecordID), strings.TrimSpace(source.FieldKey)
	if !principal.Known || objectKey == "" || recordID == "" || fieldKey == "" || strings.TrimSpace(source.Filename) == "" {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(apperror.New(apperror.KindBadRequest, "agent.task.attachment_source_invalid", nil, nil))
	}
	record, err := h.records.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(err)
	}
	fileID, ok := record.Data[fieldKey].(string)
	fileID = strings.TrimSpace(fileID)
	if !ok || record.Deleted || record.ID != recordID || record.WorkspaceID != principal.WorkspaceID || fileID == "" {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(apperror.New(apperror.KindForbidden, "agent.task.attachment_source_denied", nil, nil))
	}
	filename := strings.TrimSpace(source.Filename)
	if recorded, exists := record.Data["filename"]; exists {
		authoritative, valid := recorded.(string)
		authoritative = strings.TrimSpace(authoritative)
		if !valid || authoritative == "" || authoritative != filename {
			return agentsdk.TaskAttachment{}, runtimeAgentSDKError(apperror.New(apperror.KindForbidden, "agent.task.attachment_source_mismatch", nil, nil))
		}
	}
	resolved, err := h.attachmentFiles.Open(ctx, uploadapplication.AgentTaskAttachmentFileRequest{
		WorkspaceID: principal.WorkspaceID, OwnerUserID: record.OwnerUserID,
		ObjectKey: objectKey, RecordID: recordID, FieldKey: fieldKey, FileID: fileID,
		Filename: filename, MaxBytes: agentsdk.TaskAttachmentMaxBytes,
	})
	if err != nil {
		return agentsdk.TaskAttachment{}, runtimeAgentSDKError(err)
	}
	return agentsdk.TaskAttachment{
		Filename: resolved.Filename, ContentType: resolved.ContentType, Bytes: resolved.Bytes,
		SHA256: resolved.SHA256, Detail: source.Detail, Data: resolved.Data,
	}, nil
}

func runtimeAgentSDKError(err error) error {
	if err == nil {
		return nil
	}
	var sdkError *agentsdk.Error
	if errors.As(err, &sdkError) {
		return err
	}
	return &agentsdk.Error{Class: string(apperror.KindOf(err)), Code: apperror.CodeOf(err), Cause: err}
}

func (h runtimeAgentInteractiveHost) InvokeInteractiveTool(ctx context.Context, request agentmodulehost.InteractiveToolInvocationRequest) (agentmodulehost.InteractiveToolInvocationResult, error) {
	return h.tools.InvokeInteractiveHost(ctx, request)
}

func (h runtimeAgentInteractiveHost) StartInteractiveWorkflow(ctx context.Context, request agentmodulehost.InteractiveWorkflowHandoffRequest) (string, error) {
	if h.workflows == nil {
		return "", apperror.New(apperror.KindUnavailable, "agent.interactive.workflow_handoff_unavailable", nil, nil)
	}
	payload := make(map[string]any, len(request.Input)+2)
	for key, value := range request.Input {
		payload[key] = value
	}
	payload["agent_handoff_idempotency_key"] = strings.TrimSpace(request.IdempotencyKey)
	payload["agent_interactive_run_id"] = strings.TrimSpace(request.InteractiveID)
	result, err := h.workflows.RunAgentWorkflow(ctx, request.WorkflowKey, payload, runtimeAgentPrincipal(request.Principal))
	if err != nil {
		return "", err
	}
	processID := strings.TrimSpace(result.Execution.ProcessID)
	if processID == "" {
		return "", apperror.New(apperror.KindConflict, "agent.interactive.workflow_process_required", nil, nil)
	}
	return processID, nil
}

func (h runtimeAgentInteractiveHost) WakeAgentTask(context.Context, string, string) {}

func runtimeAgentPrincipal(value agentmodulehost.Principal) principalmodel.Principal {
	return principalmodel.Principal{
		Principal: identitysdk.Principal{Known: value.Known, WorkspaceID: strings.TrimSpace(value.WorkspaceID), UserID: strings.TrimSpace(value.UserID), RoleKey: strings.TrimSpace(value.RoleKey), AuthorizationRevision: strings.TrimSpace(value.AuthorizationRevision)},
		RequestID: strings.TrimSpace(value.RequestID), CorrelationID: strings.TrimSpace(value.CorrelationID), CausationID: strings.TrimSpace(value.CausationID),
	}
}

func runtimeAgentHostPrincipal(value principalmodel.Principal) agentmodulehost.Principal {
	return agentmodulehost.Principal{
		Known: value.Known, WorkspaceID: value.WorkspaceID, UserID: value.UserID, RoleKey: value.RoleKey,
		AuthorizationRevision: value.AuthorizationRevision, RequestID: value.RequestID,
		CorrelationID: value.CorrelationID, CausationID: value.CausationID,
	}
}

func (a *httpServerAssembly) wireNotificationHandlers() {
	// Integration management and webhook ingress are exposed by the Integration
	// owner in both Module and SaaS topologies. Runtime only exposes its durable
	// outbound handoff worker.
	if a.publications != nil || a.dependencies.NotificationInboxActions != nil {
		a.handlers.Notifications = notificationhttp.NewNotificationsHandler(notificationhttp.NotificationsDependencies{
			DeliveryLedger: a.publications,
			ActionResolver: a.dependencies.NotificationInboxActions,
			Principal:      a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
			WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
			Authenticated: a.identityHTTP.AuthenticatedFunc,
		})
	}
}
