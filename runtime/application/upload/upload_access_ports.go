package upload

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type CatalogPort interface {
	ObjectMap(context.Context) map[string]definitionmodel.ObjectSchema
}

type AuditPort interface {
	AppendWithMetadata(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

type RecordQueryPort interface {
	GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
}
