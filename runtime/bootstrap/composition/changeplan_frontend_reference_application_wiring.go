package composition

import (
	"context"

	changeplanbusiness "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func changePlanFrontendEntries(source []deploymentmodel.FrontendCapabilitySupportEntry) []changeplanmodel.FrontendSupportEntry {
	entries := make([]changeplanmodel.FrontendSupportEntry, 0, len(source))
	for _, entry := range source {
		entries = append(entries, changeplanmodel.FrontendSupportEntry{
			SupportKey: entry.SupportKey, CapabilityKeys: append([]string(nil), entry.CapabilityKeys...),
			Route: entry.Route, RequiredPermissions: append([]string(nil), entry.RequiredPermissions...), FeatureModule: entry.FeatureModule,
			AcceptanceTests: append([]string(nil), entry.AcceptanceTests...),
			ActorRoles:      append([]string(nil), entry.ActorRoles...), BusinessObjects: append([]string(nil), entry.BusinessObjects...),
			ImplementedActions: append([]string(nil), entry.ImplementedActions...),
			ReportKeys:         append([]string(nil), entry.ReportKeys...), FieldKeys: append([]string(nil), entry.FieldKeys...),
			AcceptanceClaims: append([]string(nil), entry.AcceptanceClaims...),
		})
	}
	return entries
}

func (runtime businessReferenceRuntimePortAdapter) WorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return runtime.source.WorkflowProcesses(ctx, principal, filter)
}

func (runtime businessReferenceRuntimePortAdapter) PublishedSchedulerDefinitions(ctx context.Context, principal principalmodel.Principal) ([]recordmodel.Record, error) {
	return runtime.source.PublishedSchedulerDefinitions(ctx, principal)
}

func (runtime businessReferenceRuntimePortAdapter) SnapshotObjectRecords(ctx context.Context, objectKey string, principal principalmodel.Principal, limit int) ([]recordmodel.Record, error) {
	return runtime.source.snapshotObjectRecords(ctx, objectKey, principal, limit)
}

func (runtime businessReferenceRuntimePortAdapter) ListIntegrationOutboxMessages(ctx context.Context, status, connectorKey string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return runtime.source.ListIntegrationOutboxMessages(ctx, status, connectorKey, limit, principal)
}

func assembleChangePlanReferenceApplication(schema CapabilityAuthoringSchemaProvider, runtime BusinessReferenceRuntimeProvider, evidence changeplanrepository.ChangePlanEvidenceRepository, frontend *deploymentapplication.DeploymentFrontendCapabilityApplicationService) *changeplanbusiness.ChangePlanReferenceApplicationService {
	var runtimePort changeplanbusiness.ReferenceRuntime
	if runtime != nil {
		runtimePort = businessReferenceRuntimePortAdapter{source: runtime}
	}
	var frontendPort changeplanbusiness.FrontendSnapshotSource
	if frontend != nil && frontend.Configured() {
		frontendPort = businessReferenceFrontendPortAdapter{source: frontend}
	}
	owner := changeplanbusiness.NewChangePlanReferenceApplicationService(func(ctx context.Context, principal principalmodel.Principal) changeplanbusiness.ReferenceSchema {
		if schema == nil {
			return changeplanbusiness.ReferenceSchema{}
		}
		snapshot := schema.SchemaForPrincipal(ctx, principal)
		return changeplanbusiness.ReferenceSchema{
			Objects: snapshot.Objects, Actions: snapshot.Actions, Workflows: snapshot.Workflows,
			AutomationRules: snapshot.AutomationRules, Reports: snapshot.Reports, Integrations: snapshot.Integrations,
			Agents:          snapshot.Agents,
			ProfileBindings: snapshot.IdentityProfileExtensions,
		}
	}, runtimePort, evidence, frontendPort)
	return owner
}
