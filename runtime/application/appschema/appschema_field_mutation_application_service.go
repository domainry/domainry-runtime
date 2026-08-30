package appschema

import (
	"context"
	"encoding/json"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type ApplicationSchemaRecordPageReader func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)

func ApplicationSchemaNormalizeFieldMutation(ctx context.Context, request appschemamodel.ApplicationDefinitionUpsertRequest, objects []definitionmodel.ObjectSchema, allowedFieldTypes []string, listRecords ApplicationSchemaRecordPageReader) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
	normalized, err := appschemavalidation.ApplicationSchemaNormalizeFieldMutation(request, objects, allowedFieldTypes, nil, 0)
	if err != nil || listRecords == nil {
		return normalized, err
	}
	var field definitionmodel.FieldSchema
	// ApplicationSchemaNormalizeFieldMutation returns a validated payload produced by
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
	return appschemavalidation.ApplicationSchemaNormalizeFieldMutation(normalized, objects, allowedFieldTypes, records, len(records))
}
