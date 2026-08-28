package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func authoringWorkflowDomain() capabilitycontract.CapabilityAuthoringDomain {
	return workflowpolicy.WorkflowAuthoringDomain()
}
