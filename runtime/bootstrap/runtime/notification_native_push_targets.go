package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const nativePushTargetPageSize = 20

type notificationNativePushRecordReader interface {
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}

type notificationNativePushTargetBinding struct {
	definition runtimeext.NotificationNativePushTargetDefinition
	object     definitionmodel.ObjectSchema
}

type notificationNativePushTargetResolver struct {
	records  notificationNativePushRecordReader
	bindings []notificationNativePushTargetBinding
}

func newNotificationNativePushTargetResolver(records notificationNativePushRecordReader, objects []definitionmodel.ObjectSchema, definitions []runtimeext.NotificationNativePushTargetDefinition) (*notificationNativePushTargetResolver, error) {
	if len(definitions) == 0 {
		return nil, nil
	}
	if records == nil {
		return nil, fmt.Errorf("notification native push target record reader is required")
	}
	objectIndex := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		objectIndex[strings.TrimSpace(object.Key)] = object
	}
	result := &notificationNativePushTargetResolver{records: records, bindings: make([]notificationNativePushTargetBinding, 0, len(definitions))}
	for _, definition := range definitions {
		object, found := objectIndex[strings.TrimSpace(definition.ObjectKey)]
		if !found {
			return nil, fmt.Errorf("notification native push target %s references unknown object %s", definition.Key, definition.ObjectKey)
		}
		if err := validateNotificationNativePushTargetObject(object, definition); err != nil {
			return nil, fmt.Errorf("notification native push target %s: %w", definition.Key, err)
		}
		result.bindings = append(result.bindings, notificationNativePushTargetBinding{definition: definition, object: object})
	}
	return result, nil
}

func validateNotificationNativePushTargetObject(object definitionmodel.ObjectSchema, definition runtimeext.NotificationNativePushTargetDefinition) error {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	recipient, recipientFound := fields[definition.RecipientUserField]
	platform, platformFound := fields[definition.PlatformField]
	token, tokenFound := fields[definition.TokenField]
	status, statusFound := fields[definition.StatusField]
	updated, updatedFound := fields[definition.UpdatedAtField]
	if !recipientFound || recipient.Type != "user" {
		return fmt.Errorf("recipient user field must be a user field")
	}
	if !platformFound || platform.Type != "select" || !nativePushContains(platform.Validation.Options, "android") || !nativePushContains(platform.Validation.Options, "ios") {
		return fmt.Errorf("platform field must allow android and ios")
	}
	if !tokenFound || (token.Type != "text" && token.Type != "long_text") || !token.Sensitive {
		return fmt.Errorf("token field must be sensitive text")
	}
	if !statusFound || status.Type != "select" || !nativePushContains(status.Validation.Options, definition.ActiveStatus) {
		return fmt.Errorf("status field must allow active status %s", definition.ActiveStatus)
	}
	if !updatedFound || updated.Type != "datetime" {
		return fmt.Errorf("updated-at field must be datetime")
	}
	return nil
}

func (r *notificationNativePushTargetResolver) FindNativePushToken(ctx context.Context, workspaceID, userID, provider string) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	platform := ""
	switch strings.TrimSpace(provider) {
	case "fcm":
		platform = "android"
	case "apns":
		platform = "ios"
	default:
		return "", false, fmt.Errorf("notification native push provider %q is unsupported", provider)
	}
	workspaceID, userID = strings.TrimSpace(workspaceID), strings.TrimSpace(userID)
	if workspaceID == "" || userID == "" {
		return "", false, nil
	}
	for _, binding := range r.bindings {
		definition := binding.definition
		page, err := r.records.ListRecords(ctx, workspaceID, binding.object, recordmodel.RecordListQuery{
			Page: 1, PageSize: nativePushTargetPageSize, SkipTotal: true, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
			Filters:      map[string]any{definition.RecipientUserField: userID, definition.PlatformField: platform, definition.StatusField: definition.ActiveStatus},
			Sort:         []recordmodel.RecordSortRule{{Field: definition.UpdatedAtField, Direction: "desc"}, {Field: recordmodel.IDField, Direction: "desc"}},
			SelectFields: []string{definition.TokenField, definition.PlatformField, definition.StatusField, definition.UpdatedAtField},
		})
		if err != nil {
			return "", false, err
		}
		for _, record := range page.Items {
			token := strings.TrimSpace(nativePushString(record.Data[definition.TokenField]))
			if token != "" {
				return token, true, nil
			}
		}
	}
	return "", false, nil
}

func nativePushContains(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(expected) {
			return true
		}
	}
	return false
}

func nativePushString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
