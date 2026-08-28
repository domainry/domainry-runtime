// Schema domain service tests.
package service

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

type schemaServiceProviderStub struct {
	snapshot metadatamodel.MetadataSchemaSnapshot
}

func (s schemaServiceProviderStub) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	result := s.snapshot
	if principal.Known && !principal.Allows("customer", "read") {
		result.Objects = nil
	}
	return result
}

func TestMetadataSchemaDomainServiceOwnsSnapshotProjection(t *testing.T) {
	service := NewMetadataSchemaDomainService(schemaServiceProviderStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}}, nil)
	if snapshot := service.Snapshot(t.Context()); len(snapshot.Objects) != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
