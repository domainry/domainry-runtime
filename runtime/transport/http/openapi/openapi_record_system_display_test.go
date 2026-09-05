package openapi

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestRecordSchemaPublishesReadOnlySystemIdentityDisplayNames(t *testing.T) {
	schemas := openAPISchemas(appschemamodel.ApplicationSchemaSnapshot{})
	recordSchema := schemas["Record"].(map[string]any)
	properties := recordSchema["properties"].(map[string]any)
	for _, key := range []string{
		"create_by", "create_by_name", "update_by", "update_by_name",
		"owner_user_id", "owner_user_name", "owner_org_id", "owner_org_name",
	} {
		property, ok := properties[key].(map[string]any)
		if !ok || property["readOnly"] != true {
			t.Fatalf("Record.%s schema = %#v", key, properties[key])
		}
	}
}
