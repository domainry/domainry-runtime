package catalog

import (
	"reflect"
	"testing"
)

func TestToolCatalogIsClosedAndPublishesPrerequisites(t *testing.T) {
	if !reflect.DeepEqual(Keys(), []string{"callConnector", "createRecord", "deleteRecord", "get_record", "invoke_action", "query_records", "readRecord", "sendEmail", "sendMessage", "updateRecord"}) {
		t.Fatalf("keys=%v", Keys())
	}
	if _, exists := Lookup("unknown"); exists {
		t.Fatal("unknown tool entered the catalog")
	}
	invoke, exists := Lookup(" invoke_action ")
	if !exists || !invoke.Writes || !invoke.RequiresAllowedActions || !invoke.RequiresAllowedObjects {
		t.Fatalf("invoke_action=%+v exists=%v", invoke, exists)
	}
}
