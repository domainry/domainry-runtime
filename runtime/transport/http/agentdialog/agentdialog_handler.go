package agentdialog

import (
	"context"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	"net/http"
	"strings"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

type AgentDialogContextResolver interface {
	ResolveGlobalContext(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error)
}

type AgentInteractiveExecutor interface {
	Execute(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error)
}

type AgentInteractiveRunReader interface {
	Get(context.Context, string, principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error)
}

type agentTaskRunService interface {
	List(context.Context, string, agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error)
	Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error)
	RequestCancel(context.Context, string, string, string) (agentmodel.AgentTaskRun, bool, error)
	Operate(context.Context, string, string, string, string, string, principalmodel.Principal) (agentmodel.AgentTaskRun, bool, error)
}

type agentTaskToolService interface {
	Invoke(context.Context, agentruntime.AgentToolInvocationRequest) (agentruntime.AgentToolInvocationResult, error)
}

type Config struct {
	RateLimitPerMinute int
}

type AgentDialogHandler struct {
	sessions                  *agentapplication.AgentApplicationService
	proposalState             *agentapplication.AgentApplicationService
	proposalDecisions         *agentapplication.AgentProposalApplicationService
	analysisCatalog           *appschemaapplication.ApplicationSchemaQueryApplicationService
	analysisRecords           *recordapplication.RecordApplicationService
	diagnosticAudit           *auditapplication.AuditApplicationService
	reportGovernance          *agentapplication.AgentApplicationService
	contextResolver           AgentDialogContextResolver
	taskRuns                  agentTaskRunService
	operations                *operationsapplication.OperationsApplicationService
	taskTools                 agentTaskToolService
	interactiveRuns           AgentInteractiveRunReader
	interactive               AgentInteractiveExecutor
	config                    Config
	rateLimiter               ratelimit.Limiter
	principal                 func(*http.Request) principalmodel.Principal
	writeJSON                 func(http.ResponseWriter, int, any)
	writeError                func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError         func(http.ResponseWriter, *http.Request, error)
	decodeJSON                func(http.ResponseWriter, *http.Request, any) bool
	admin                     func(http.HandlerFunc) http.HandlerFunc
	securityAudit             func(*http.Request, string, string, map[string]any)
	securityAuditForPrincipal func(*http.Request, principalmodel.Principal, string, string, map[string]any)
}

type AgentDialogDependencies struct {
	Sessions                  *agentapplication.AgentApplicationService
	ProposalState             *agentapplication.AgentApplicationService
	ProposalDecisions         *agentapplication.AgentProposalApplicationService
	AnalysisCatalog           *appschemaapplication.ApplicationSchemaQueryApplicationService
	AnalysisRecords           *recordapplication.RecordApplicationService
	DiagnosticAudit           *auditapplication.AuditApplicationService
	ReportGovernance          *agentapplication.AgentApplicationService
	ContextResolver           AgentDialogContextResolver
	TaskRuns                  *agentruntime.AgentTaskRunApplicationService
	Operations                *operationsapplication.OperationsApplicationService
	TaskTools                 *agentruntime.AgentToolGateway
	InteractiveRuns           AgentInteractiveRunReader
	Interactive               AgentInteractiveExecutor
	Config                    Config
	RateLimiter               ratelimit.Limiter
	Principal                 func(*http.Request) principalmodel.Principal
	WriteJSON                 func(http.ResponseWriter, int, any)
	WriteError                func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError         func(http.ResponseWriter, *http.Request, error)
	DecodeJSON                func(http.ResponseWriter, *http.Request, any) bool
	Admin                     func(http.HandlerFunc) http.HandlerFunc
	SecurityAudit             func(*http.Request, string, string, map[string]any)
	SecurityAuditForPrincipal func(*http.Request, principalmodel.Principal, string, string, map[string]any)
}

func NewAgentDialogHandler(deps AgentDialogDependencies) *AgentDialogHandler {
	return &AgentDialogHandler{
		sessions: deps.Sessions, proposalState: deps.ProposalState, proposalDecisions: deps.ProposalDecisions,
		analysisCatalog: deps.AnalysisCatalog, analysisRecords: deps.AnalysisRecords,
		diagnosticAudit: deps.DiagnosticAudit, reportGovernance: deps.ReportGovernance,
		contextResolver: deps.ContextResolver,
		taskRuns:        deps.TaskRuns, taskTools: deps.TaskTools, operations: deps.Operations,
		interactiveRuns: deps.InteractiveRuns, interactive: deps.Interactive,
		config: deps.Config, rateLimiter: deps.RateLimiter,
		principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError,
		writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON, admin: deps.Admin,
		securityAudit: deps.SecurityAudit, securityAuditForPrincipal: deps.SecurityAuditForPrincipal,
	}
}

func (h *AgentDialogHandler) UseRateLimiter(limiter ratelimit.Limiter) {
	if limiter != nil {
		h.rateLimiter = limiter
	}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func workspaceIDFromRequest(r *http.Request) string {
	if workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); workspaceID != "" {
		return workspaceID
	}
	return "default"
}

func cloneStringAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
