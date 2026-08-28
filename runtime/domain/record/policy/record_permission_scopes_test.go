package policy

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import "testing"

func TestOwnerFieldKeyPrefersBusinessRequesterOverWorkflowAssignee(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "current_approver", Type: "user"},
		{Key: "requester", Type: "user"},
	}}
	if got := RecordOwnerFieldKey(object); got != "requester" {
		t.Fatalf("RecordOwnerFieldKey() = %q, want requester", got)
	}
}

func TestOwnerFieldKeyKeepsExplicitOwnerAheadOfRequester(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "requester", Type: "user"},
		{Key: "owner", Type: "user"},
	}}
	if got := RecordOwnerFieldKey(object); got != "owner" {
		t.Fatalf("RecordOwnerFieldKey() = %q, want owner", got)
	}
}
