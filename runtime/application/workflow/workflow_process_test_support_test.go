package workflow

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type workflowRegistryStub struct {
	items   map[string]definitionmodel.WorkflowSchema
	deleted string
}

func (r *workflowRegistryStub) List() []definitionmodel.WorkflowSchema {
	items := make([]definitionmodel.WorkflowSchema, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}
	return items
}

func (r *workflowRegistryStub) Get(key string) (definitionmodel.WorkflowSchema, bool) {
	item, ok := r.items[key]
	return item, ok
}

func (r *workflowRegistryStub) Set(key string, workflow definitionmodel.WorkflowSchema) {
	if r.items == nil {
		r.items = map[string]definitionmodel.WorkflowSchema{}
	}
	r.items[key] = workflow
}

func (r *workflowRegistryStub) Delete(key string) {
	delete(r.items, key)
	r.deleted = key
}

func (r *workflowRegistryStub) Count() int { return len(r.items) }

func workflowAdminPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
}
