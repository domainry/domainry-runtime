package composition

import (
	"context"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func assembleAutomationApplication(records *runtimeAssembly) *automationapplication.AutomationApplicationService {
	metadata := records.applicationSchemaService
	return automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{
		Rules:               runtimeAutomationRuleRegistry{records: records},
		Connectors:          records.connectorRegistry,
		RecordRepository:    records.recordRepo,
		WorkerStore:         records.automationWorkerRepo,
		ExecutionRepository: records.automationExecutionRepo,
		DeliveryRepository:  records.integrationDeliveryRepo,
		ConfigRepository:    records.integrationConfigRepo,
		Audit:               records.auditApplicationService.AppendWithMetadata,
		Principal: func(ctx context.Context, userID, roleKey, fallbackRoleKey string) principalmodel.Principal {
			if roleKey == "" {
				roleKey = fallbackRoleKey
			}
			principal, err := resolveIdentityPrincipal(ctx, records.agentPrincipals, userID, roleKey)
			if err != nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			return principal
		},
		Schema: records.SchemaForPrincipal,
		InvokeAction: func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return records.actionService.Invoke(ctx, actionmodel.ActionSourceAutomation, invocation)
		},
		Workflows:    records.Applications().Workflows,
		Metadata:     metadata,
		CanAccess:    records.RecordQueryPolicyDomainService.CanAccessRecord,
		ValidateRule: metadata.ValidateAutomationRuleDefinition,
		AuthoringProjection: func() capabilitycontract.CapabilityAuthoringProjection {
			return capabilityapplication.RuntimeAuthoringProjection("automation")
		},
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
