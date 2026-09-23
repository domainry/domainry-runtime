package appschema

import (
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

// ApplicationSchemaApplicationService owns Runtime-specific metadata
// validation and lifecycle orchestration. Definition, localization and
// dictionary reads are owned by the Metadata SDK Binding.
type ApplicationSchemaApplicationService struct {
	repository   appschemarepository.ApplicationSchemaRepository
	runtime      RuntimeSchema
	records      recordrepository.RecordRepository
	integrations integrationsdk.Management
}

type RuntimeSchema interface {
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

type ApplicationSchemaDependencies struct {
	Repository   appschemarepository.ApplicationSchemaRepository
	Runtime      RuntimeSchema
	Records      recordrepository.RecordRepository
	Integrations integrationsdk.Management
}

func NewApplicationSchemaApplicationService(dependencies ApplicationSchemaDependencies) *ApplicationSchemaApplicationService {
	return &ApplicationSchemaApplicationService{
		repository: dependencies.Repository, runtime: dependencies.Runtime,
		records: dependencies.Records, integrations: dependencies.Integrations,
	}
}
