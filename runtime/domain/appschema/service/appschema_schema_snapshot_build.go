package service

import (
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"crypto/sha256"
	"encoding/json"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func SchemaSnapshotHash(snapshot appschemamodel.ApplicationSchemaSnapshot) string {
	snapshot.SchemaHash, snapshot.SnapshotVersion = "", ""
	payload, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("%x", hash[:])[:16]
}

func CloneIntegrationSchema(value connectormodel.IntegrationSchema) connectormodel.IntegrationSchema {
	return connectormodel.IntegrationSchema{
		Connectors:    append([]connectormodel.ConnectorSchema(nil), value.Connectors...),
		Connections:   append([]connectormodel.ConnectionSchema(nil), value.Connections...),
		EventMappings: append([]connectormodel.IntegrationEventMappingSchema(nil), value.EventMappings...),
	}
}

func GuardedWriteContracts(actions []definitionmodel.ActionSchema) []appschemamodel.ApplicationSchemaGuardedWriteContract {
	_ = actions
	return []appschemamodel.ApplicationSchemaGuardedWriteContract{}
}
