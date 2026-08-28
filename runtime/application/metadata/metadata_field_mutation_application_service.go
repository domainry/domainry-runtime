package metadata

import (
	"context"
	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type MetadataRecordPageReader func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)

func MetadataNormalizeFieldMutation(ctx context.Context, request metadatamodel.MetadataDefinitionUpsertRequest, objects []definitionmodel.ObjectSchema, allowedFieldTypes []string, listRecords MetadataRecordPageReader) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
	normalized, err := metadatavalidation.MetadataNormalizeFieldMutation(request, objects, allowedFieldTypes, nil, 0)
	if err != nil || listRecords == nil {
		return normalized, err
	}
	var field definitionmodel.FieldSchema
	// MetadataNormalizeFieldMutation returns a validated payload produced by
	// json.Marshal, so decoding the same FieldSchema cannot fail here.
	_ = json.Unmarshal(normalized.Payload, &field)
	objectKey := strings.TrimSpace(normalized.ObjectKey)
	if objectKey == "" {
		objectKey = normalizedDefinitionValue(field.Config["_definition_object_key"])
	}
	var object definitionmodel.ObjectSchema
	for _, candidate := range objects {
		if strings.TrimSpace(candidate.Key) == objectKey {
			object = candidate
			break
		}
	}
	existingRequired := false
	for _, existing := range object.Fields {
		if strings.TrimSpace(existing.Key) == strings.TrimSpace(field.Key) {
			existingRequired = existing.Required
			break
		}
	}
	if !field.Required || field.Default != nil || field.DefaultValue != nil || existingRequired {
		return normalized, nil
	}
	records := []recordmodel.Record{}
	afterID := ""
	for {
		page, err := listRecords(ctx, object, recordmodel.RecordListQuery{Page: 1, PageSize: 200, SkipTotal: true, AfterID: afterID, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
		if err != nil {
			return normalized, err
		}
		records = append(records, page.Items...)
		if !page.HasNext {
			break
		}
		afterID = page.Items[len(page.Items)-1].ID
	}
	return metadatavalidation.MetadataNormalizeFieldMutation(normalized, objects, allowedFieldTypes, records, len(records))
}
