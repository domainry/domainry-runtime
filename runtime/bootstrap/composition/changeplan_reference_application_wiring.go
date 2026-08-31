package composition

import (
	"context"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"

	changeplanbusiness "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type businessReferenceRuntimeAdapter struct {
	records   *runtimeAssembly
	workflows *workflowapplication.WorkflowApplicationService
}

func (a businessReferenceRuntimeAdapter) WorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	if a.workflows == nil || a.records == nil || a.records.workflowProcessRepo == nil {
		return []workflowmodel.WorkflowProcessInstance{}, nil
	}
	return a.workflows.WorkflowProcesses(ctx, principal, filter)
}

func (a businessReferenceRuntimeAdapter) PublishedSchedulerDefinitions(ctx context.Context, principal principalmodel.Principal) ([]recordmodel.Record, error) {
	if a.records == nil || a.records.schedulerService == nil {
		return []recordmodel.Record{}, nil
	}
	definitions, err := a.records.schedulerService.PublishedDefinitions(ctx, principal)
	return schedulerPublishedDefinitionRecords(definitions), err
}

func (a businessReferenceRuntimeAdapter) ListPublicationMessages(ctx context.Context, status, connectorKey string, limit int, principal principalmodel.Principal) ([]publicationmodel.Message, error) {
	if a.records == nil || a.records.publicationHandoffService == nil || a.records.publicationRepository == nil {
		return []publicationmodel.Message{}, nil
	}
	return a.records.publicationHandoffService.ListPublicationMessages(ctx, connectorKey, status, limit, principal)
}

type BusinessReferenceRuntimeProvider interface {
	WorkflowProcesses(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	PublishedSchedulerDefinitions(context.Context, principalmodel.Principal) ([]recordmodel.Record, error)
	ListPublicationMessages(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error)
}

type businessReferenceRuntimePortAdapter struct {
	source BusinessReferenceRuntimeProvider
}

func (runtime businessReferenceRuntimePortAdapter) WorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return runtime.source.WorkflowProcesses(ctx, principal, filter)
}

func (runtime businessReferenceRuntimePortAdapter) PublishedSchedulerDefinitions(ctx context.Context, principal principalmodel.Principal) ([]recordmodel.Record, error) {
	return runtime.source.PublishedSchedulerDefinitions(ctx, principal)
}

func (runtime businessReferenceRuntimePortAdapter) ListPublicationMessages(ctx context.Context, status, connectorKey string, limit int, principal principalmodel.Principal) ([]publicationmodel.Message, error) {
	return runtime.source.ListPublicationMessages(ctx, status, connectorKey, limit, principal)
}

func assembleChangePlanReferenceApplication(schema CapabilityAuthoringSchemaProvider, runtime BusinessReferenceRuntimeProvider, evidence changeplanrepository.ChangePlanEvidenceRepository) *changeplanbusiness.ChangePlanReferenceApplicationService {
	var runtimePort changeplanbusiness.ReferenceRuntime
	if runtime != nil {
		runtimePort = businessReferenceRuntimePortAdapter{source: runtime}
	}
	return changeplanbusiness.NewChangePlanReferenceApplicationService(func(ctx context.Context, principal principalmodel.Principal) changeplanbusiness.ReferenceSchema {
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
	}, runtimePort, evidence)
}
