package upload

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func uploadFileMatchesRecord(identifier string, field definitionmodel.FieldSchema, value any) (recordmodel.RecordFileReference, bool) {
	identifier = strings.TrimSpace(identifier)
	references, err := recordmodel.RecordFileReferences(field, value)
	if err != nil {
		return recordmodel.RecordFileReference{}, false
	}
	for _, reference := range references {
		if reference.FileID == identifier {
			return reference, true
		}
	}
	return recordmodel.RecordFileReference{}, false
}

func uploadRecordBoolean(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}
