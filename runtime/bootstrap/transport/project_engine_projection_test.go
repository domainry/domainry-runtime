package transport

import (
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestProjectEngineRecordPreservesReadOnlyOwnerDisplay(t *testing.T) {
	record := recordmodel.Record{
		ID: "task-1", Data: map[string]any{"owner": "user-1"},
		OwnerUserID: "user-1", OwnerUserName: "销售甲",
	}
	projected := projectEngineRecord("crm_task", record)
	if projected.OwnerUserID != "user-1" || projected.OwnerUserName != "销售甲" {
		t.Fatalf("owner display projection = %+v", projected)
	}
	if _, present := projected.Fields["owner_user_name"]; present {
		t.Fatal("display name must not become a mutable business field")
	}
}
