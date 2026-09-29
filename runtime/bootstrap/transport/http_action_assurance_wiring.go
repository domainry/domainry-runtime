package transport

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
)

func (a *httpServerAssembly) actionAssuranceApplication(actions *actionapplication.ActionApplicationService) *actionapplication.ActionAssuranceApplicationService {
	if a == nil {
		return nil
	}
	return projectActionAssuranceApplication(a.dependencies, actions)
}

func projectActionAssuranceApplication(dependencies HTTPServerDependencies, actions *actionapplication.ActionApplicationService) *actionapplication.ActionAssuranceApplicationService {
	if dependencies.Store == nil || dependencies.IdentityBinding == nil {
		return nil
	}
	binding, ok := dependencies.IdentityBinding.(identitysdk.ActionAssuranceBinding)
	if !ok || binding.ActionAssurance() == nil {
		return nil
	}
	grants := actionservice.NewActionAssuranceDomainService(actionpersistence.NewActionAssuranceStore(dependencies.Store), nil)
	return actionapplication.NewActionAssuranceApplicationService(actions, grants, binding.ActionAssurance())
}
