package composition

import (
	"context"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func assembleAutomationApplication(records *runtimeAssembly) *automationapplication.AutomationApplicationService {
	metadata := records.applicationSchemaService
	return automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{
		Rules:                 runtimeAutomationRuleRegistry{records: records},
		Connectors:            records.connectorRegistry,
		RecordRepository:      records.recordRepo,
		WorkerStore:           records.automationWorkerRepo,
		ExecutionRepository:   records.automationExecutionRepo,
		DeliveryRepository:    records.publicationRepository,
		IntegrationManagement: records.integrationOwnerManagement,
		IntegrationOperations: records.integrationOwnerOperations,
		Audit: func(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
			records.auditApplicationService.AppendWithMetadata(ctx, auditmodel.EventFamilyBusinessEntity, event, objectKey, recordID, principal, summary, before, after, metadata)
		},
		Principal: func(ctx context.Context, userID, roleKey, fallbackRoleKey string) principalmodel.Principal {
			if roleKey == "" {
				roleKey = fallbackRoleKey
			}
			principal, err := resolveIdentityPrincipal(ctx, records.identityPrincipals, userID, roleKey)
			if err != nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			return principal
		},
		Schema: records.SchemaForPrincipal,
		InvokeAction: func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return records.actionService.Invoke(ctx, actionmodel.ActionSourceAutomation, invocation)
		},
		Workflows:             records.Applications().Workflows,
		CanAccess:             records.RecordQueryPolicyDomainService.CanAccessRecord,
		MutationScope:         records.RecordQueryPolicyDomainService.MutationScopeExpression,
		ValidateRule:          metadata.ValidateAutomationRuleDefinition,
		Worker:                records.workerDependencies,
		NotificationCompiler:  records.automationNotificationCompiler,
		NotificationCommitter: records.automationNotificationCommitter,
	})
}

type runtimeAutomationRuleRegistry struct{ records *runtimeAssembly }

func (r runtimeAutomationRuleRegistry) List() []automationmodel.AutomationRuleSchema {
	r.records.mu.RLock()
	defer r.records.mu.RUnlock()
	rules := make([]automationmodel.AutomationRuleSchema, 0, len(r.records.automationRules))
	for _, rule := range r.records.automationRules {
		rules = append(rules, rule)
	}
	return rules
}

func (r runtimeAutomationRuleRegistry) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	r.records.mu.RLock()
	defer r.records.mu.RUnlock()
	rule, ok := r.records.automationRules[strings.TrimSpace(key)]
	return rule, ok
}
