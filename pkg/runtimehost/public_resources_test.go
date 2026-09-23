package runtimehost

import (
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

func TestAttachPublicResourcesUsesCodeOwnedDefinitions(t *testing.T) {
	model := projectmodel.RuntimeModel{Objects: []definitionmodel.ObjectSchema{{Key: "card"}}}
	definitions := []runtimeext.PublicResourceDefinition{{
		ObjectKey: "card",
		Resource: definitionmodel.ObjectPublicResource{
			Key: "public_card", AccessKeyField: "share_key", StateField: "status", ActiveState: "published", Fields: []string{"name"},
		},
	}}
	if err := attachPublicResources(&model, definitions); err != nil {
		t.Fatal(err)
	}
	if len(model.Objects[0].PublicResources) != 1 || model.Objects[0].PublicResources[0].Key != "public_card" {
		t.Fatalf("public resources=%#v", model.Objects[0].PublicResources)
	}
	definitions[0].Resource.Key = "changed"
	if model.Objects[0].PublicResources[0].Key != "public_card" {
		t.Fatal("attached public resource did not preserve the registered value")
	}
}

func TestAttachPublicResourcesRejectsUnknownObject(t *testing.T) {
	err := attachPublicResources(&projectmodel.RuntimeModel{}, []runtimeext.PublicResourceDefinition{{
		ObjectKey: "missing", Resource: definitionmodel.ObjectPublicResource{Key: "public_card"},
	}})
	if err == nil {
		t.Fatal("expected unknown object rejection")
	}
}
