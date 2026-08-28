package service

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestBuildSchemaSnapshotCanonicalizesOwnerProjection(t *testing.T) {
	snapshot := BuildSchemaSnapshot(SchemaSnapshotState{
		TemplateID: "template", TemplateVersion: "1",
		Objects: []definitionmodel.ObjectSchema{{Key: "z"}, {Key: "a"}},
		Actions: []definitionmodel.ActionSchema{{Key: "z.run", ObjectKey: "z"}, {Key: "a.run", ObjectKey: "a"}},
	})
	if snapshot.Objects[0].Key != "a" || snapshot.Actions[0].Key != "a.run" {
		t.Fatalf("snapshot is not canonical: %#v", snapshot)
	}
	if snapshot.SchemaHash == "" || snapshot.SnapshotVersion != snapshot.SchemaHash {
		t.Fatalf("snapshot hash is incomplete: %#v", snapshot)
	}
}
