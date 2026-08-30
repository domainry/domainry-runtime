package composition

import (
	"context"

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
)

func newDeploymentFrontendCapabilityApplicationService(repository deploymentrepository.DeploymentFrontendCapabilityRepository, businessBindings func(context.Context) deploymentapplication.FrontendBusinessBindings) *deploymentapplication.DeploymentFrontendCapabilityApplicationService {
	return deploymentapplication.NewDeploymentFrontendCapabilityApplicationService(deploymentapplication.FrontendCapabilityDependencies{
		Repository:       repository,
		ContractVersion:  capabilitycontract.RuntimeAuthoringContractVersion,
		Capabilities:     frontendCapabilityDefinitions,
		BusinessBindings: businessBindings,
	})
}

func frontendCapabilityDefinitions() []deploymentmodel.FrontendCapabilityDefinition {
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	definitions := []deploymentmodel.FrontendCapabilityDefinition{}
	for _, capabilityDomain := range contract.Domains {
		for _, capability := range capabilityDomain.Capabilities {
			definitions = append(definitions, deploymentmodel.FrontendCapabilityDefinition{Key: capability.Key, FrontendSupportKey: capability.FrontendSupportKey, Permissions: append([]string(nil), capability.Permissions...)})
		}
	}
	return definitions
}

func frontendBusinessBindings(schema CanonicalSchemaProvider) deploymentapplication.FrontendBusinessBindings {
	bindings := deploymentapplication.FrontendBusinessBindings{Objects: map[string]bool{}, Actions: map[string]bool{}, Reports: map[string]bool{}, Fields: map[string]bool{}}
	if schema == nil {
		return bindings
	}
	snapshot := schema.Schema()
	bindings.SchemaHash = snapshot.SchemaHash
	bindings.SchemaSnapshotVersion = snapshot.SnapshotVersion
	for _, object := range snapshot.Objects {
		bindings.Objects[object.Key] = true
		for _, field := range object.Fields {
			bindings.Fields[object.Key+"."+field.Key] = true
		}
	}
	for _, action := range snapshot.Actions {
		bindings.Actions[action.Key] = true
	}
	for _, report := range snapshot.Reports {
		bindings.Reports[report.Key] = true
	}
	return bindings
}
