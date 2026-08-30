// Schema domain service tests.
package service

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type schemaServiceProviderStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s schemaServiceProviderStub) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	result := s.snapshot
	if principal.Known && !principal.Allows("customer", "read") {
		result.Objects = nil
	}
	return result
}

func TestApplicationSchemaDomainServiceOwnsSnapshotProjection(t *testing.T) {
	service := NewApplicationSchemaDomainService(schemaServiceProviderStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}}, nil)
	if snapshot := service.Snapshot(t.Context()); len(snapshot.Objects) != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
