package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestOwnershipFieldsAreFixedRuntimeColumns(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "requester", Type: "user"},
		{Key: "owner", Type: "user"},
	}}
	if got := RecordOwnerFieldKey(object); got != "owner_user_id" {
		t.Fatalf("RecordOwnerFieldKey() = %q", got)
	}
	if got := RecordOwnerOrgIDFieldKey(object); got != "owner_org_id" {
		t.Fatalf("RecordOwnerOrgIDFieldKey() = %q", got)
	}
}
