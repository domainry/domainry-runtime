package runtimehost

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

// attachPublicResources projects validated code-owned definitions onto the
// Runtime's private object representation. The source model remains free of
// executable/publication behavior.
func attachPublicResources(model *projectmodel.RuntimeModel, definitions []runtimeext.PublicResourceDefinition) error {
	if model == nil {
		return fmt.Errorf("attach public resources: runtime model is nil")
	}
	objects := make(map[string]int, len(model.Objects))
	for index, object := range model.Objects {
		objects[strings.TrimSpace(object.Key)] = index
	}
	for _, definition := range definitions {
		index, found := objects[strings.TrimSpace(definition.ObjectKey)]
		if !found {
			return fmt.Errorf("attach public resource %s: object %s is unavailable", definition.Resource.Key, definition.ObjectKey)
		}
		model.Objects[index].PublicResources = append(model.Objects[index].PublicResources, definition.Resource)
	}
	return nil
}
