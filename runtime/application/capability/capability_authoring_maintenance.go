package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	deploymentcontract "github.com/domainry/domainry-runtime/runtime/domain/deployment/contract"
	metadatacontract "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
)

// authoringMaintenanceDomain is an aggregation boundary only. Each capability
// definition is published by the business owner that models and validates it.
func authoringMaintenanceDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "maintenance", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{
		changeplancontract.ChangePlanCurrentStateSnapshotAuthoringCapability(),
		changeplancontract.ChangePlanReferenceImpactAuthoringCapability(),
		changeplancontract.ChangePlanValidationAuthoringCapability(),
		deploymentcontract.DeploymentFrontendSupportObservationAuthoringCapability(),
		changeplancontract.ChangePlanApplyAuthoringCapability(),
		metadatacontract.MetadataRollbackAuthoringCapability(),
		changeplancontract.ChangePlanRollbackPolicyAuthoringCapability(),
	}}
}
