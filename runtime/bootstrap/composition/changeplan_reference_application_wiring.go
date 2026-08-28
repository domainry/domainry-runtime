package composition

import (
	"context"

	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
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
	return a.records.schedulerService.PublishedDefinitions(ctx, principal)
}

func (a businessReferenceRuntimeAdapter) snapshotObjectRecords(ctx context.Context, objectKey string, principal principalmodel.Principal, limit int) ([]recordmodel.Record, error) {
	return a.records.businessSystemService.SnapshotObjectRecords(ctx, objectKey, principal, limit)
}

func (a businessReferenceRuntimeAdapter) ListIntegrationOutboxMessages(ctx context.Context, status, connectorKey string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if a.records == nil || a.records.integrationService == nil || a.records.integrationDeliveryRepo == nil {
		return []integrationmodel.IntegrationOutboxMessage{}, nil
	}
	return a.records.integrationService.ListIntegrationOutboxMessages(ctx, status, connectorKey, limit, principal)
}

type BusinessReferenceRuntimeProvider interface {
	WorkflowProcesses(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	PublishedSchedulerDefinitions(context.Context, principalmodel.Principal) ([]recordmodel.Record, error)
	snapshotObjectRecords(context.Context, string, principalmodel.Principal, int) ([]recordmodel.Record, error)
	ListIntegrationOutboxMessages(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
}

type businessReferenceRuntimePortAdapter struct {
	source BusinessReferenceRuntimeProvider
}

type businessReferenceFrontendPortAdapter struct {
	source *deploymentapplication.DeploymentFrontendCapabilityApplicationService
}

func (frontend businessReferenceFrontendPortAdapter) FrontendReferenceSnapshot(ctx context.Context, principal principalmodel.Principal) (changeplanmodel.FrontendCapabilities, error) {
	snapshot, err := frontend.source.Snapshot(ctx, principal)
	if err != nil {
		return changeplanmodel.FrontendCapabilities{}, err
	}
	return changePlanFrontendCapabilities(snapshot), nil
}

func changePlanFrontendCapabilities(snapshot deploymentmodel.FrontendCapabilitySnapshot) changeplanmodel.FrontendCapabilities {
	result := changeplanmodel.FrontendCapabilities{Revision: snapshot.Revision, UpdatedAt: snapshot.UpdatedAt, Status: snapshot.Status, ManifestHash: snapshot.ManifestHash}
	for _, requirement := range snapshot.MissingFrontendSupport {
		result.MissingFrontendSupport = append(result.MissingFrontendSupport, changeplanmodel.FrontendRequirement{CapabilityKey: requirement.CapabilityKey, SupportKey: requirement.SupportKey})
	}
	result.StaleFrontendSupport = changePlanFrontendEntries(snapshot.StaleFrontendSupport)
	if snapshot.Manifest == nil {
		return result
	}
	manifest := &changeplanmodel.FrontendManifest{
		ManifestVersion: snapshot.Manifest.ManifestVersion, FrontendVersion: snapshot.Manifest.FrontendVersion,
		RuntimeContractVersions: append([]string(nil), snapshot.Manifest.RuntimeContractVersions...), Entries: changePlanFrontendEntries(snapshot.Manifest.Entries),
	}
	if evidence := snapshot.Manifest.DeploymentEvidence; evidence != nil {
		manifest.DeploymentEvidence = &changeplanmodel.FrontendDeploymentEvidence{
			AuditContractVersion: evidence.AuditContractVersion, DesignContractHash: evidence.DesignContractHash,
			RouteRegistryHash: evidence.RouteRegistryHash, FrontendSourceHash: evidence.FrontendSourceHash, AuditArtifactHash: evidence.AuditArtifactHash,
		}
	}
	result.Manifest = manifest
	return result
}
