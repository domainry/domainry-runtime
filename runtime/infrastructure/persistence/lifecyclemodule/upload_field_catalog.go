package lifecyclemodule

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type UploadFieldCatalog struct {
	objects map[string]map[string]struct{}
}

func NewUploadFieldCatalog(objects []definitionmodel.ObjectSchema) UploadFieldCatalog {
	result := UploadFieldCatalog{objects: make(map[string]map[string]struct{}, len(objects))}
	for _, object := range objects {
		fields := make(map[string]struct{}, len(object.Fields))
		for _, field := range object.Fields {
			kind := strings.TrimSpace(field.Type)
			if key := strings.TrimSpace(field.Key); key != "" && (kind == recordmodel.RecordFileFieldType || kind == recordmodel.RecordFileListFieldType) {
				fields[key] = struct{}{}
			}
		}
		result.objects[strings.TrimSpace(object.Key)] = fields
	}
	return result
}

func (c UploadFieldCatalog) HasUploadField(objectKey, fieldKey string) bool {
	fields := c.objects[strings.TrimSpace(objectKey)]
	_, found := fields[strings.TrimSpace(fieldKey)]
	return found
}
