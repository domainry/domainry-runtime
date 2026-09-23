package projectmodel

import "testing"

func TestCompileProducesStableStorageOnlyRuntimeModel(t *testing.T) {
	model, err := Decode([]byte(validModelJSON()))
	if err != nil {
		t.Fatal(err)
	}
	model.Roles["viewer"] = Role{Key: "viewer", Name: "Viewer"}
	runtimeModel, err := Compile(model)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeModel.ProjectKey != model.Project.Key || runtimeModel.ContentHash == "" || len(runtimeModel.Objects) != len(model.Objects) {
		t.Fatalf("runtime model = %#v", runtimeModel)
	}
	if got := runtimeModel.Roles[0].Key; got != "administrator" {
		t.Fatalf("roles are not stable-key ordered: %q", got)
	}
}
