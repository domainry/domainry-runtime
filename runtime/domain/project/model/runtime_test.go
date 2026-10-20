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

func TestCompileCarriesExplicitSubjectErasureDefaultAndFieldOverride(t *testing.T) {
	model, err := Decode([]byte(validModelJSON()))
	if err != nil {
		t.Fatal(err)
	}
	model.Project.SubjectErasureDefault = "retain"
	field := model.Objects["customer"].Fields["name"]
	field.LifecycleErase = "anonymize"
	object := model.Objects["customer"]
	object.Fields["name"] = field
	model.Objects["customer"] = object
	objects, err := RuntimeObjects(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, compiled := range objects {
		if compiled.Key != "customer" {
			continue
		}
		for _, compiledField := range compiled.Fields {
			if compiledField.Key == "name" && compiledField.Config["lifecycle_erase"] != "anonymize" {
				t.Fatalf("name lifecycle config=%#v", compiledField.Config)
			}
			if compiledField.Key != "name" && compiledField.Config["lifecycle_erase"] != "retain" {
				t.Fatalf("%s lifecycle config=%#v", compiledField.Key, compiledField.Config)
			}
		}
		return
	}
	t.Fatal("customer object not compiled")
}

func TestCompileProducesIdentityOnlyRuntimeModel(t *testing.T) {
	model, err := Decode([]byte(`{
  "schema_version":"1",
  "project":{"key":"identity_shell","name":"Identity Shell","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`))
	if err != nil {
		t.Fatal(err)
	}
	runtimeModel, err := Compile(model)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeModel.Objects) != 0 || runtimeModel.ContentHash == "" || runtimeModel.InitialWorkspaceAdministratorRole != "administrator" {
		t.Fatalf("runtime model = %#v", runtimeModel)
	}
}
