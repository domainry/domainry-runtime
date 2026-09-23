package composition

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
)

func assembleApplicationSchema(records *runtimeAssembly) *appschemaapplication.ApplicationSchemaApplicationService {
	if records == nil {
		return appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{})
	}
	if records.applicationSchemaService != nil {
		return records.applicationSchemaService
	}
	return appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{
		Repository: records.applicationSchemaRepo, Runtime: records,
		Records: records.recordRepo, Integrations: records.integrationOwnerManagement,
	})
}
