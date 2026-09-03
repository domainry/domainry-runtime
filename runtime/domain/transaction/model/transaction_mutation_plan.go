package transactionmodel

import (
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"reflect"
	"slices"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type MutationWriteReference struct {
	ObjectKey string
	RecordID  string
	Fields    []string
}

type ObjectWritePolicy string

const (
	ObjectWritePolicyDirectCRUD ObjectWritePolicy = "direct_crud"
	ObjectWritePolicyActionOnly ObjectWritePolicy = "action_only"
)

func MutationObjectWritePolicy(object definitionmodel.ObjectSchema) ObjectWritePolicy {
	if value, ok := object.Config["write_policy"].(string); ok && strings.TrimSpace(value) == string(ObjectWritePolicyActionOnly) {
		return ObjectWritePolicyActionOnly
	}
	return ObjectWritePolicyDirectCRUD
}

type MutationPlan struct {
	mutationContext MutationContext
	commit          RecordMutationCommit
	before          map[string]any
	writeSet        []MutationWriteReference
}

type MutationPlanError struct {
	Code  string
	Field string
}

func (e *MutationPlanError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Field)
}

func NewMutationPlan(mutationContext MutationContext, commit RecordMutationCommit, before map[string]any) (MutationPlan, error) {
	operation := strings.TrimSpace(commit.Operation)
	if operation != "create" && operation != "update" && operation != "delete" && operation != "restore" {
		return MutationPlan{}, mutationPlanError("operation")
	}
	objectKey := strings.TrimSpace(commit.Object.Key)
	if objectKey == "" {
		return MutationPlan{}, mutationPlanError("object_key")
	}
	recordID := strings.TrimSpace(commit.Record.ID)
	if recordID == "" {
		recordID = strings.TrimSpace(commit.RecordID)
	}
	if recordID == "" {
		return MutationPlan{}, mutationPlanError("record_id")
	}
	if err := mutationValidatePredicates(commit.Object, commit.Predicates); err != nil {
		return MutationPlan{}, err
	}
	if mutationContext.WorkspaceID() == "" {
		return MutationPlan{}, mutationPlanError("mutation_context")
	}
	fields := mutationPlanFields(operation, commit.Record.Data, before)
	if mutationContext.HasEffectAuthority() {
		for _, field := range fields {
			if !mutationContext.AllowsEffect(objectKey, field) {
				return MutationPlan{}, &MutationPlanError{Code: "backend.mutation.effect_authority_denied", Field: objectKey + "." + field}
			}
		}
	}
	clonedCommit := mutationCloneCommit(commit)
	clonedCommit.Operation = operation
	clonedCommit.RecordID = recordID
	return MutationPlan{
		mutationContext: mutationContext,
		commit:          clonedCommit,
		before:          mutationCloneMap(before),
		writeSet:        []MutationWriteReference{{ObjectKey: objectKey, RecordID: recordID, Fields: slices.Clone(fields)}},
	}, nil
}

func mutationValidatePredicates(object definitionmodel.ObjectSchema, predicates []MutationPredicate) error {
	fields := make(map[string]bool, len(object.Fields)+1)
	fields["updated_at"] = true
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = true
	}
	for _, predicate := range predicates {
		field := strings.TrimSpace(predicate.Field)
		if field == "" || !fields[field] {
			return &MutationPlanError{Code: "backend.mutation.predicate_invalid", Field: field}
		}
		switch strings.TrimSpace(predicate.Operator) {
		case "eq", "ne", "lt", "lte", "gt", "gte":
		default:
			return &MutationPlanError{Code: "backend.mutation.predicate_invalid", Field: field + ".operator"}
		}
		if code := strings.TrimSpace(predicate.ErrorCode); code != "" && !mutationStableCode(code) {
			return &MutationPlanError{Code: "backend.mutation.predicate_invalid", Field: field + ".error_code"}
		}
	}
	return nil
}

func mutationStableCode(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return value != ""
}

func (p MutationPlan) Context() MutationContext { return p.mutationContext }

func (p MutationPlan) CanonicalCommit() RecordMutationCommit { return mutationCloneCommit(p.commit) }

func (p MutationPlan) Before() map[string]any { return mutationCloneMap(p.before) }

func (p MutationPlan) WriteSet() []MutationWriteReference {
	result := make([]MutationWriteReference, len(p.writeSet))
	for index := range p.writeSet {
		result[index] = p.writeSet[index]
		result[index].Fields = slices.Clone(p.writeSet[index].Fields)
	}
	return result
}

func mutationPlanFields(operation string, data, before map[string]any) []string {
	if operation == "delete" {
		return []string{"*"}
	}
	fields := make([]string, 0, len(data))
	for field, value := range data {
		if operation != "create" && reflect.DeepEqual(value, before[field]) {
			continue
		}
		if field = strings.TrimSpace(field); field != "" {
			fields = append(fields, field)
		}
	}
	slices.Sort(fields)
	return fields
}

func mutationCloneCommit(commit RecordMutationCommit) RecordMutationCommit {
	result := commit
	result.Object = mutationCloneObjectSchema(commit.Object)
	result.Record.Data = mutationCloneMap(commit.Record.Data)
	result.Conditions = mutationCloneMap(commit.Conditions)
	result.Predicates = append([]MutationPredicate(nil), commit.Predicates...)
	for index := range result.Predicates {
		result.Predicates[index].Value = mutationCloneValue(commit.Predicates[index].Value)
	}
	result.AuthorizationScope = mutationCloneAuthorizationScope(commit.AuthorizationScope)
	if commit.Audit != nil {
		audit := *commit.Audit
		audit.Before = mutationCloneMap(commit.Audit.Before)
		audit.After = mutationCloneMap(commit.Audit.After)
		audit.Metadata = mutationCloneMap(commit.Audit.Metadata)
		result.Audit = &audit
	}
	result.Audits = append(result.Audits[:0:0], commit.Audits...)
	for index := range result.Audits {
		result.Audits[index].Before = mutationCloneMap(commit.Audits[index].Before)
		result.Audits[index].After = mutationCloneMap(commit.Audits[index].After)
		result.Audits[index].Metadata = mutationCloneMap(commit.Audits[index].Metadata)
	}
	result.Outbox = append([]publicationmodel.Message(nil), commit.Outbox...)
	for index := range result.Outbox {
		result.Outbox[index].Payload = mutationCloneMap(commit.Outbox[index].Payload)
	}
	result.WorkflowIntents = append([]workflowmodel.WorkflowExecution(nil), commit.WorkflowIntents...)
	for index := range result.WorkflowIntents {
		result.WorkflowIntents[index].Action = mutationCloneMap(commit.WorkflowIntents[index].Action)
		result.WorkflowIntents[index].Payload = mutationCloneMap(commit.WorkflowIntents[index].Payload)
		result.WorkflowIntents[index].Result = mutationCloneMap(commit.WorkflowIntents[index].Result)
	}
	result.LocalizedValues = append([]recordmodel.RecordLocalizedValueMutation(nil), commit.LocalizedValues...)
	return result
}

func mutationCloneAuthorizationScope(scope *recordmodel.RecordScopeExpression) *recordmodel.RecordScopeExpression {
	if scope == nil {
		return nil
	}
	result := *scope
	result.Values = slices.Clone(scope.Values)
	result.Path = slices.Clone(scope.Path)
	result.Children = make([]recordmodel.RecordScopeExpression, len(scope.Children))
	for index := range scope.Children {
		child := mutationCloneAuthorizationScope(&scope.Children[index])
		result.Children[index] = *child
	}
	return &result
}

func mutationCloneObjectSchema(object definitionmodel.ObjectSchema) definitionmodel.ObjectSchema {
	result := object
	result.I18n = mutationCloneLocalizedText(object.I18n)
	result.UX = mutationCloneMap(object.UX)
	result.Config = mutationCloneMap(object.Config)
	result.Fields = append(result.Fields[:0:0], object.Fields...)
	for index := range result.Fields {
		result.Fields[index].I18n = mutationCloneLocalizedText(object.Fields[index].I18n)
		result.Fields[index].Config = mutationCloneMap(object.Fields[index].Config)
		result.Fields[index].Validation.Options = slices.Clone(object.Fields[index].Validation.Options)
		result.Fields[index].Options = mutationCloneValue(object.Fields[index].Options)
		result.Fields[index].Default = mutationCloneValue(object.Fields[index].Default)
		result.Fields[index].DefaultValue = mutationCloneValue(object.Fields[index].DefaultValue)
	}
	result.Validations = append(result.Validations[:0:0], object.Validations...)
	for index := range result.Validations {
		result.Validations[index].Fields = slices.Clone(object.Validations[index].Fields)
		result.Validations[index].I18n = mutationCloneLocalizedText(object.Validations[index].I18n)
		result.Validations[index].Config = mutationCloneMap(object.Validations[index].Config)
	}
	return result
}

func mutationCloneLocalizedText(value map[string]map[string]string) map[string]map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]map[string]string, len(value))
	for namespace, messages := range value {
		cloned := make(map[string]string, len(messages))
		for locale, message := range messages {
			cloned[locale] = message
		}
		result[namespace] = cloned
	}
	return result
}

func mutationCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = mutationCloneValue(item)
	}
	return result
}

func mutationCloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return mutationCloneMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = mutationCloneValue(typed[index])
		}
		return result
	case []string:
		return slices.Clone(typed)
	case map[string]string:
		result := make(map[string]string, len(typed))
		for key, item := range typed {
			result[key] = item
		}
		return result
	default:
		return value
	}
}

func mutationPlanError(field string) error {
	return &MutationPlanError{Code: "backend.mutation.plan_invalid", Field: field}
}
