package capability

import (
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestSchedulerObjectSchemaMapsBooleanParameter(t *testing.T) {
	schema := schedulerObjectSchema([]capabilitycontract.CapabilityAuthoringParameter{{Key: "enabled", Type: "boolean", Required: true}})
	if schema == nil || schema.Properties["enabled"].Type != "boolean" || len(schema.Required) != 1 || schema.Required[0] != "enabled" {
		t.Fatalf("schema=%#v", schema)
	}
}
