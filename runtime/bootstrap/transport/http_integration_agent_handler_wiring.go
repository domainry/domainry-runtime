package transport

import (
	"context"
	"crypto/sha256"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	agentdialoghttp "github.com/domainry/domainry-runtime/runtime/transport/http/agentdialog"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
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

func (a *httpServerAssembly) wireIntegrationAndAgentHandlers(agentConfig runtimehttp.AgentHTTPConfig) {
	a.handlers.Integrations = integrationhttp.NewIntegrationsHandler(integrationhttp.IntegrationsDependencies{
		Connections: a.integrations, Bindings: a.integrations,
		RuntimeExecution: a.integrations, Webhooks: a.integrations, Operations: a.operations,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
		DecodeJSON: a.callbacks.DecodeJSON, Admin: a.identityHTTP.PermissionFunc("workspace.admin"),
		Authenticated: a.identityHTTP.AuthenticatedFunc, Entrypoint: a.identityHTTP.PermissionFunc("integration.entrypoint.invoke"),
		Locale: a.callbacks.Locale, Identity: a.dependencies.IdentityBinding,
		IdentityAudience: a.dependencies.Config.IdentityAudience, ProductName: a.dependencies.Config.EffectiveProductBrandName(),
	})
	state, proposals := assembleAgentApplicationPorts(a.dependencies, a.principals)
	// assembleAgentApplicationPorts owns the pair invariant: both ports are
	// either available from one persistent store or both absent.
	if state != nil {
		contextResolver := agentapplication.NewAgentAuthorizationApplicationService(agentapplication.AgentAuthorizationDependencies{
			Principals: a.principals,
			Schema:     a.dependencies.Records,
			Records:    agentRecordVisibilityAdapter{records: a.recordQueries},
		})
		credentialKey := sha256.Sum256([]byte("domainry-agent-task-credential-v1:" + a.dependencies.Config.IntegrationSecretKey))
		credentials := agentapplication.NewAgentTaskCredentialApplicationService(credentialKey[:], nil, nil)
		taskTools := agentapplication.NewAgentToolGateway(agentapplication.AgentToolGatewayDependencies{
			Authorization: contextResolver, Credentials: credentials,
			Queries: agentTaskToolQueryAdapter{records: a.recordQueries}, Actions: agentTaskToolActionAdapter{actions: a.dependencies.Records.Applications().Actions},
			Proposals: agentTaskToolProposalAdapter{state: state}, Risk: agentTaskToolRiskAdapter{actions: a.dependencies.Records.Applications().Actions},
			Ledger: agentpersistence.NewAgentTaskRunStore(a.dependencies.Store), RateLimiter: a.dependencies.RateLimiter,
			InteractiveRuns: a.dependencies.Records.Applications().AgentInteractiveRuns,
			TaskRuns:        a.dependencies.Records.Applications().AgentTasks,
		})
		interactive := newAgentInteractiveExecution(a.dependencies.Records.Applications().NewAgentInteractive, taskTools)
		a.handlers.AgentDialog = agentdialoghttp.NewAgentDialogHandler(agentdialoghttp.AgentDialogDependencies{
			Sessions: state, ProposalState: state, ProposalDecisions: proposals,
			AnalysisCatalog: a.dependencies.Records.Applications().Schema,
			AnalysisRecords: a.recordQueries,
			DiagnosticAudit: a.dependencies.Records.Applications().Audit, ReportGovernance: state,
			ContextResolver: contextResolver,
			TaskRuns:        a.dependencies.Records.Applications().AgentTasks, TaskTools: taskTools,
			Operations:      a.operations,
			InteractiveRuns: a.dependencies.Records.Applications().AgentInteractiveRuns,
			Interactive:     interactive,
			Config: agentdialoghttp.Config{
				BaseURL: agentConfig.BaseURL, APIKey: agentConfig.APIKey, AgentID: agentConfig.AgentID,
				Timeout: agentConfig.Timeout, RateLimitPerMinute: agentConfig.RateLimitPerMinute,
			},
			RateLimiter: a.dependencies.RateLimiter, Principal: a.callbacks.Principal,
			WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
			WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
			Admin: a.identityHTTP.PermissionFunc("workspace.admin"), SecurityAudit: a.callbacks.SecurityAudit,
			SecurityAuditForPrincipal: a.callbacks.SecurityAuditForPrincipal,
		})
	}
	if a.dependencies.Notifications != nil {
		a.handlers.Notifications = notificationhttp.NewNotificationsHandler(notificationhttp.NotificationsDependencies{
			Management: a.dependencies.Notifications, Delivery: a.dependencies.Notifications, Inbox: a.dependencies.Notifications,
			DeliveryLedger: a.integrations,
			Principal:      a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
			WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
			DecodeJSON: a.callbacks.DecodeJSON, Authenticated: a.identityHTTP.AuthenticatedFunc,
		})
	}
}

func newAgentInteractiveExecution(factory func(*agentapplication.AgentToolGateway) *agentapplication.AgentInteractiveExecutionApplicationService, tools *agentapplication.AgentToolGateway) *agentapplication.AgentInteractiveExecutionApplicationService {
	if factory == nil {
		return nil
	}
	return factory(tools)
}
