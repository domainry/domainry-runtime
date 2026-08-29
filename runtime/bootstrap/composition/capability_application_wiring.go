package composition

import (
	"context"

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type CapabilityAuthoringSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
}

// CanonicalSchemaProvider exposes Runtime-owned schema facts to internal
// composition. It must never be replaced with an anonymous-principal
// projection: authorization filters are applied only at the consuming
// application boundary.
type CanonicalSchemaProvider interface {
	Schema() metadatamodel.ApplicationSchemaSnapshot
}

func newCapabilityAuthoringApplicationService(schema CapabilityAuthoringSchemaProvider) *capabilityapplication.CapabilityAuthoringApplicationService {
	return capabilityapplication.NewCapabilityAuthoringApplicationService(func(ctx context.Context, principal principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		if schema == nil {
			return capabilitycontract.CapabilityInstanceSchema{}
		}
		snapshot := schema.SchemaForPrincipal(ctx, principal)
		return capabilitycontract.CapabilityInstanceSchema{Objects: snapshot.Objects, Actions: snapshot.Actions, Workflows: snapshot.Workflows, Reports: snapshot.Reports, Integrations: snapshot.Integrations}
	})
}
