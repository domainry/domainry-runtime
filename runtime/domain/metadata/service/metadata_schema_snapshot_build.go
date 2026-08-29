package service

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	"crypto/sha256"
	"encoding/json"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func SchemaSnapshotHash(snapshot metadatamodel.ApplicationSchemaSnapshot) string {
	snapshot.SchemaHash, snapshot.SnapshotVersion = "", ""
	payload, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("%x", hash[:])[:16]
}

func CloneIntegrationSchema(value integrationmodel.IntegrationSchema) integrationmodel.IntegrationSchema {
	return integrationmodel.IntegrationSchema{
		Connectors:    append([]integrationmodel.ConnectorSchema(nil), value.Connectors...),
		Connections:   append([]integrationmodel.ConnectionSchema(nil), value.Connections...),
		EventMappings: append([]integrationmodel.IntegrationEventMappingSchema(nil), value.EventMappings...),
	}
}

func GuardedWriteContracts(actions []definitionmodel.ActionSchema) []metadatamodel.MetadataGuardedWriteContract {
	_ = actions
	return []metadatamodel.MetadataGuardedWriteContract{}
}
