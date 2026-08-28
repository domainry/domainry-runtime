package integration

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type IntegrationOwnerTaskRecordLookup func(context.Context, string, string, string) (recordmodel.Record, bool)

type IntegrationOwnerTaskProjectionRequest struct {
	Activity  definitionmodel.ObjectSchema
	Event     integrationmodel.IntegrationEvent
	Mapping   integrationmodel.IntegrationEventMappingSchema
	Payload   map[string]any
	Principal principalmodel.Principal
	Related   map[string]string
	Now       time.Time
}

// IntegrationOwnerTaskReferenceCandidates extracts references that composition may enrich
// through Record lookups. It performs no repository access.
func IntegrationOwnerTaskReferenceCandidates(payload map[string]any) (map[string]string, string) {
	related := map[string]string{
		"related_customer":    payloadFirstString(payload, "related_customer", "customer_id", "customer"),
		"related_contact":     payloadFirstString(payload, "related_contact", "contact_id", "contact"),
		"related_opportunity": payloadFirstString(payload, "related_opportunity", "opportunity_id", "opportunity"),
	}
	if targetObject := payloadFirstString(payload, "target_object", "object_key"); targetObject != "" {
		if targetID := payloadFirstString(payload, "target_record_id", "record_id"); targetID != "" {
			switch targetObject {
			case "customer":
				if related["related_customer"] == "" {
					related["related_customer"] = targetID
				}
			case "contact":
				if related["related_contact"] == "" {
					related["related_contact"] = targetID
				}
			case "opportunity":
				if related["related_opportunity"] == "" {
					related["related_opportunity"] = targetID
				}
			}
		}
	}
	return related, payloadFirstString(payload, "contact_email", "customer_email", "email", "from.email", "sender.email")
}

// IntegrationEnrichOwnerTaskReferences owns which Record relations may be inferred from
// an inbound payload. Composition supplies only the Record lookup port.
func IntegrationEnrichOwnerTaskReferences(ctx context.Context, payload map[string]any, lookup IntegrationOwnerTaskRecordLookup) map[string]string {
	related, matchEmail := IntegrationOwnerTaskReferenceCandidates(payload)
	if lookup == nil || matchEmail == "" {
		return related
	}
	if related["related_contact"] == "" {
		if contact, ok := lookup(ctx, "contact", "email", matchEmail); ok {
			related["related_contact"] = contact.ID
			if related["related_customer"] == "" {
				related["related_customer"] = strings.TrimSpace(fmt.Sprint(contact.Data["customer"]))
			}
		}
	}
	if related["related_customer"] == "" {
		if customer, ok := lookup(ctx, "customer", "email", matchEmail); ok {
			related["related_customer"] = customer.ID
		}
	}
	return related
}

// IntegrationBuildOwnerTaskProjection owns the deterministic payload-to-activity mapping.
// Record lookup and creation remain composition responsibilities.
func IntegrationBuildOwnerTaskProjection(req IntegrationOwnerTaskProjectionRequest) (map[string]any, error) {
	if strings.TrimSpace(req.Activity.Key) == "" {
		return nil, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.integration.event_mapping.owner_task_activity_missing"}
	}
	owner := payloadFirstString(req.Payload, "owner", "owner_id", "assignee", "external_actor_id")
	if owner == "" {
		owner = strings.TrimSpace(req.Principal.UserID)
	}
	if owner == "" {
		return nil, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.event_mapping.owner_task_missing_owner"}
	}
	subject := payloadFirstString(req.Payload, "task_subject", "subject", "title", "summary")
	if subject == "" {
		subject = "External follow-up: " + strings.TrimSpace(req.Event.EventType)
	}
	if strings.TrimSpace(subject) == "External follow-up:" {
		subject = "External follow-up: " + strings.TrimSpace(req.Event.Provider)
	}
	data := map[string]any{"subject": truncateString(subject, 160)}
	for _, field := range ownerTaskFieldKeys(req.Activity, "owner_id", "owner", "assignee_id", "assignee") {
		data[field] = owner
	}
	department := ownerTaskDepartmentValue(req.Payload, req.Principal)
	for _, field := range ownerTaskFieldKeys(req.Activity, "department_id", "owner_department_id", "department", "dept_id") {
		data[field] = department
	}
	if ownerTaskFieldExists(req.Activity, "activity_type") {
		data["activity_type"] = ownerTaskActivityType(req.Activity, req.Payload)
	}
	if ownerTaskFieldExists(req.Activity, "status") {
		data["status"] = ownerTaskSelectValue(req.Activity, "status", []string{"pending", "open", "todo", "new"}, "pending")
	}
	if dueKey := ownerTaskFieldKey(req.Activity, "due_at", "due_date", "dueDate"); dueKey != "" {
		field, _ := ownerTaskField(req.Activity, dueKey)
		data[dueKey] = ownerTaskDueValue(req.Payload, field, req.Now)
	}
	if notesKey := ownerTaskFieldKey(req.Activity, "notes", "content", "body", "description"); notesKey != "" {
		data[notesKey] = ownerTaskNotes(req.Event, req.Mapping, req.Payload, req.Related)
	}
	for logicalKey, value := range req.Related {
		if strings.TrimSpace(value) == "" {
			continue
		}
		var candidates []string
		switch logicalKey {
		case "related_customer":
			candidates = []string{"related_customer", "customer_id", "customer"}
		case "related_contact":
			candidates = []string{"related_contact", "contact_id", "contact"}
		case "related_opportunity":
			candidates = []string{"related_opportunity", "opportunity_id", "opportunity"}
		}
		if field := ownerTaskFieldKey(req.Activity, candidates...); field != "" {
			data[field] = value
		}
	}
	return data, nil
}

func ownerTaskFieldExists(object definitionmodel.ObjectSchema, key string) bool {
	_, ok := ownerTaskField(object, key)
	return ok
}

func ownerTaskFieldKey(object definitionmodel.ObjectSchema, candidates ...string) string {
	for _, candidate := range candidates {
		if ownerTaskFieldExists(object, candidate) {
			return candidate
		}
	}
	return ""
}

func ownerTaskFieldKeys(object definitionmodel.ObjectSchema, candidates ...string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, candidate := range candidates {
		if !seen[candidate] && ownerTaskFieldExists(object, candidate) {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func ownerTaskDepartmentValue(payload map[string]any, principal principalmodel.Principal) string {
	if value := payloadFirstString(payload, "department_id", "owner_department_id", "department", "dept_id"); value != "" {
		return value
	}
	if value := strings.TrimSpace(principal.DepartmentID); value != "" {
		return value
	}
	return "company"
}

func ownerTaskDueValue(payload map[string]any, field definitionmodel.FieldSchema, now time.Time) string {
	value := payloadFirstString(payload, "due_at", "due_date", "task_due_at", "task_due_date", "follow_up_at", "follow_up_date")
	if value != "" {
		if strings.EqualFold(field.Type, "datetime") {
			if parsed, err := time.Parse("2006-01-02", value); err == nil {
				return parsed.UTC().Format(time.RFC3339)
			}
		}
		return value
	}
	if now.IsZero() {
		now = time.Now()
	}
	next := now.UTC().Add(24 * time.Hour)
	if strings.EqualFold(field.Type, "date") {
		return next.Format("2006-01-02")
	}
	return next.Format(time.RFC3339)
}

func ownerTaskActivityType(activity definitionmodel.ObjectSchema, payload map[string]any) string {
	value := payloadFirstString(payload, "activity_type", "task_type")
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "call", "email", "meeting":
		return ownerTaskSelectValue(activity, "activity_type", []string{value, strings.ToLower(value)}, strings.ToLower(value))
	case "note":
		return ownerTaskSelectValue(activity, "activity_type", []string{value, "note", "other"}, "note")
	case "other":
		return ownerTaskSelectValue(activity, "activity_type", []string{value, "other"}, "other")
	}
	if value != "" {
		return ownerTaskSelectValue(activity, "activity_type", []string{value}, value)
	}
	return ownerTaskSelectValue(activity, "activity_type", []string{"task", "follow_up", "other"}, "task")
}

func ownerTaskSelectValue(object definitionmodel.ObjectSchema, fieldKey string, preferred []string, fallback string) string {
	field, ok := ownerTaskField(object, fieldKey)
	if !ok {
		return fallback
	}
	options := ownerTaskFieldOptions(field)
	if len(options) == 0 {
		return fallback
	}
	for _, candidate := range preferred {
		for _, option := range options {
			if option == candidate || strings.EqualFold(option, candidate) {
				return option
			}
		}
	}
	return options[0]
}

func ownerTaskField(object definitionmodel.ObjectSchema, key string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == key {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func ownerTaskFieldOptions(field definitionmodel.FieldSchema) []string {
	if options := compactStrings(append([]string(nil), field.Validation.Options...)); len(options) > 0 {
		return options
	}
	for _, key := range []string{"options", "value_domain_items", "valueDomainItems"} {
		if options := dictionaryOptionKeys(field.Config[key]); len(options) > 0 {
			return options
		}
	}
	for _, key := range []string{"value_domain", "valueDomain"} {
		if valueDomain, ok := field.Config[key].(map[string]any); ok {
			if options := dictionaryOptionKeys(valueDomain["items"]); len(options) > 0 {
				return options
			}
		}
	}
	return nil
}

func dictionaryOptionKeys(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	options := []string{}
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			options = append(options, typed)
		case map[string]any:
			options = append(options, firstString(typed["value"], typed["key"], typed["label"], typed["name"]))
		}
	}
	return compactStrings(options)
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func compactStrings(values []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func ownerTaskNotes(event integrationmodel.IntegrationEvent, mapping integrationmodel.IntegrationEventMappingSchema, payload map[string]any, related map[string]string) string {
	parts := []string{"External event follow-up", "provider=" + strings.TrimSpace(event.Provider), "event_type=" + strings.TrimSpace(event.EventType), "external_id=" + strings.TrimSpace(event.ExternalID), "mapping=" + strings.TrimSpace(mapping.Key), "raw payload redacted=true"}
	if source := payloadFirstString(payload, "source", "channel"); source != "" {
		parts = append(parts, "source="+source)
	}
	if summary := payloadFirstString(payload, "message_summary", "summary", "body_preview"); summary != "" {
		parts = append(parts, "summary="+summary)
	}
	for _, key := range []string{"related_customer", "related_contact", "related_opportunity"} {
		if value := strings.TrimSpace(related[key]); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return truncateString(strings.Join(parts, "\n"), 2000)
}

func payloadFirstString(payload map[string]any, paths ...string) string {
	for _, path := range paths {
		if value := integrationPayloadPathString(payload, path); value != "" {
			return value
		}
	}
	return ""
}

func integrationPayloadPathString(payload map[string]any, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		value, ok := object[strings.TrimSpace(part)]
		if !ok || value == nil {
			return ""
		}
		current = value
	}
	return strings.TrimSpace(fmt.Sprint(current))
}

func truncateString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit])
}
