package businessseed

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

const runtimeGeneratedBusinessSeedSourceKind = "runtime_generated"

func generateManifestBusinessSeedRows(manifest manifestmodel.ManifestSchema, explicit []manifestBusinessSeedRow) ([]manifestBusinessSeedRow, error) {
	covered := map[string]bool{}
	firstSeedKeyByObject := map[string]string{}
	for _, row := range explicit {
		objectKey := strings.TrimSpace(row.ObjectKey)
		seedKey := strings.TrimSpace(row.Key)
		if objectKey == "" || seedKey == "" {
			continue
		}
		covered[objectKey] = true
		if firstSeedKeyByObject[objectKey] == "" {
			firstSeedKeyByObject[objectKey] = seedKey
		}
	}

	// Manifest Objects intentionally keep dictionary-backed value domains by
	// reference. Baseline generation needs the same effective field contract
	// used by Record/Application Schema before it can choose and validate a
	// deterministic select value.
	objects := appschemaprojection.ApplicationSchemaEnrichObjectsWithFieldValueDomains(manifest.Objects, manifest.Dictionaries)
	sort.SliceStable(objects, func(i, j int) bool { return strings.TrimSpace(objects[i].Key) < strings.TrimSpace(objects[j].Key) })
	for _, object := range objects {
		objectKey := strings.TrimSpace(object.Key)
		if objectKey == "" || covered[objectKey] {
			continue
		}
		firstSeedKeyByObject[objectKey] = runtimeGeneratedBusinessSeedKey(objectKey)
	}

	generated := make([]manifestBusinessSeedRow, 0, len(objects))
	referenceTime := time.Now().UTC()
	for _, object := range objects {
		objectKey := strings.TrimSpace(object.Key)
		if objectKey == "" || covered[objectKey] {
			continue
		}
		data, err := generateManifestBusinessSeedData(object, firstSeedKeyByObject, referenceTime)
		if err != nil {
			return nil, fmt.Errorf("generate Runtime baseline record for %s: %w", objectKey, err)
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("encode Runtime baseline record for %s: %w", objectKey, err)
		}
		generated = append(generated, manifestBusinessSeedRow{
			Key:         firstSeedKeyByObject[objectKey],
			ObjectKey:   objectKey,
			DataJSON:    string(raw),
			OwnerUserID: "admin",
			SourceKind:  runtimeGeneratedBusinessSeedSourceKind,
			SourceID:    valueOrDefault(strings.TrimSpace(manifest.TemplateID), "runtime"),
		})
	}
	return generated, nil
}

func runtimeGeneratedBusinessSeedKey(objectKey string) string {
	return "runtime_baseline_" + strings.TrimSpace(objectKey)
}

func generateManifestBusinessSeedData(object definitionmodel.ObjectSchema, targetSeedKeys map[string]string, referenceTime time.Time) (map[string]any, error) {
	data := map[string]any{}
	recordpolicy.RecordApplyFieldDefaults(object, data)
	mustPopulate := generatedSeedRequiredFields(object)
	populated := false
	for index, field := range object.Fields {
		if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.DisabledAt) != "" {
			continue
		}
		if !recordvalidation.RecordIsEmptyValue(data[field.Key]) {
			populated = true
			continue
		}
		shouldPopulate := mustPopulate[field.Key] || generatedSeedRepresentativeField(field) || !populated
		if !shouldPopulate {
			continue
		}
		value, found, err := generatedSeedFieldValue(object, field, index, targetSeedKeys, referenceTime)
		if err != nil {
			if mustPopulate[field.Key] {
				return nil, err
			}
			continue
		}
		if found {
			data[field.Key] = value
			populated = true
		} else if mustPopulate[field.Key] {
			return nil, fmt.Errorf("field %s has no safe deterministic baseline value", field.Key)
		}
	}
	generatedSeedApplyValidationHints(object, data)
	normalized, err := recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return nil, fmt.Errorf("normalize generated data: %w", err)
	}
	if err := recordvalidation.RecordValidateData(object, normalized, false); err != nil {
		return nil, fmt.Errorf("validate generated data: %w", err)
	}
	return normalized, nil
}

func generatedSeedRequiredFields(object definitionmodel.ObjectSchema) map[string]bool {
	required := map[string]bool{}
	for _, field := range object.Fields {
		if field.Required {
			required[field.Key] = true
		}
	}
	for _, validation := range object.Validations {
		if !recordvalidation.RecordValidationBlocks(validation) {
			continue
		}
		if key := strings.TrimSpace(validation.FieldKey); key != "" {
			required[key] = true
		}
		for _, key := range validation.Fields {
			if key = strings.TrimSpace(key); key != "" {
				required[key] = true
			}
		}
		for _, configKey := range []string{"required_field", "start_field", "end_field", "state_field", "when_field"} {
			if key := strings.TrimSpace(fmt.Sprint(validation.Config[configKey])); key != "" && key != "<nil>" {
				required[key] = true
			}
		}
		for _, configKey := range []string{"required_fields", "scope_fields", "fields"} {
			for _, key := range generatedSeedStringSlice(validation.Config[configKey]) {
				required[key] = true
			}
		}
	}
	return required
}

func generatedSeedRepresentativeField(field definitionmodel.FieldSchema) bool {
	if field.Type == "relation" || field.Type == "select" || field.Type == "user" {
		return true
	}
	key := generatedSeedSemanticKey(field)
	return generatedSeedContainsAny(key,
		"name", "title", "code", "number", "_no", "sku", "status", "stage", "description", "summary",
		"email", "phone", "url", "address", "city", "country", "quantity", "amount", "price", "total",
	)
}

func generatedSeedFieldValue(object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema, index int, targetSeedKeys map[string]string, referenceTime time.Time) (any, bool, error) {
	switch strings.TrimSpace(field.Type) {
	case "relation":
		target := recordvalidation.RecordRelationTarget(field)
		if seedKey := strings.TrimSpace(targetSeedKeys[target]); seedKey != "" {
			return "$record:" + seedKey, true, nil
		}
		if target == "identity_user" {
			return "admin", true, nil
		}
		// Organization units are optional and deployment-owned. Runtime cannot
		// enumerate them through the Identity SDK, so only an authored default
		// may safely populate this relation.
		if target == "identity_organization_unit" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("relation field %s has unknown target %q", field.Key, target)
	case "user":
		return "admin", true, nil
	case "select":
		value, found := generatedSeedSelectValue(object.Key, field)
		if !found {
			return nil, false, fmt.Errorf("select field %s has no declared option", field.Key)
		}
		return value, true, nil
	case "boolean":
		key := generatedSeedSemanticKey(field)
		return !generatedSeedContainsAny(key, "disabled", "deleted", "archived", "blocked", "cancelled"), true, nil
	case "integer":
		value, err := generatedSeedNumber(field, index, referenceTime, true)
		return int64(math.Round(value)), err == nil, err
	case "number":
		value, err := generatedSeedNumber(field, index, referenceTime, false)
		return value, err == nil, err
	case "currency", "percent":
		value, err := generatedSeedNumber(field, index, referenceTime, false)
		return strconv.FormatFloat(value, 'f', -1, 64), err == nil, err
	case "date":
		return generatedSeedDate(field, referenceTime).Format("2006-01-02"), true, nil
	case "datetime":
		return generatedSeedDate(field, referenceTime).Format(time.RFC3339), true, nil
	case "email":
		return "alex.chen@example.com", true, nil
	case "phone":
		return "+86 138 0013 8000", true, nil
	case "url":
		return "https://example.com/" + strings.ReplaceAll(strings.TrimSpace(object.Key), "_", "-"), true, nil
	case "text", "long_text", "":
		value, err := generatedSeedTextValue(object, field)
		return value, err == nil, err
	default:
		return nil, false, fmt.Errorf("field %s uses unsupported type %q", field.Key, field.Type)
	}
}

func generatedSeedSelectValue(objectKey string, field definitionmodel.FieldSchema) (string, bool) {
	candidates := append([]string(nil), field.Validation.Options...)
	if len(candidates) == 0 {
		candidates = recordvalidation.RecordImportValueDomainCandidates(field)
	}
	bestIndex, bestScore := -1, math.MaxInt
	for index, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		score := generatedSeedInitialOptionScore(candidate)*100 + index
		if score < bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestIndex < 0 {
		return "", false
	}
	selected := strings.TrimSpace(candidates[bestIndex])
	if value, issue := recordvalidation.RecordCoerceImportValue(objectKey, field, selected); issue == "" {
		return strings.TrimSpace(fmt.Sprint(value)), true
	}
	return selected, true
}

func generatedSeedInitialOptionScore(candidate string) int {
	normalized := strings.ToLower(strings.TrimSpace(candidate))
	if open := strings.LastIndex(normalized, "("); open >= 0 && strings.HasSuffix(normalized, ")") {
		normalized = strings.TrimSpace(normalized[open+1 : len(normalized)-1])
	}
	preferred := []string{"draft", "new", "pending", "open", "active", "enabled", "normal", "available", "queued", "planned", "created", "unassigned"}
	for index, value := range preferred {
		if normalized == value {
			return index
		}
	}
	return len(preferred) + 10
}

func generatedSeedNumber(field definitionmodel.FieldSchema, index int, referenceTime time.Time, integer bool) (float64, error) {
	key := generatedSeedSemanticKey(field)
	value := float64(index + 1)
	switch {
	case generatedSeedContainsAny(key, "on_hand", "inventory", "stock", "capacity"):
		value = 100
	case generatedSeedContainsAny(key, "reorder", "reserved", "minimum_quantity", "min_quantity"):
		value = 20
	case generatedSeedContainsAny(key, "quantity", "count", "days", "duration"):
		value = 10
	case generatedSeedContainsAny(key, "amount", "price", "cost", "budget", "credit_limit", "salary", "total"):
		value = 1000
	case generatedSeedContainsAny(key, "percent", "percentage", "discount", "rate"):
		value = 10
	case generatedSeedContainsAny(key, "priority", "sequence", "sort_order"):
		value = float64(index + 1)
	case generatedSeedContainsAny(key, "year"):
		value = float64(referenceTime.Year())
	}
	minimum, hasMinimum := generatedSeedFieldMinimum(field)
	maximum, hasMaximum := generatedSeedFieldMaximum(field)
	if field.Type == "percent" && hasMaximum && maximum <= 1 && value > maximum {
		value = 0.1
	}
	if hasMinimum && hasMaximum && minimum > maximum {
		return 0, fmt.Errorf("numeric field %s has minimum %v above maximum %v", field.Key, minimum, maximum)
	}
	if hasMinimum && value < minimum {
		value = minimum
	}
	if hasMaximum && value > maximum {
		value = maximum
	}
	if integer {
		value = math.Ceil(value)
		if hasMaximum && value > maximum {
			return 0, fmt.Errorf("integer field %s has no value inside its numeric bounds", field.Key)
		}
	}
	return value, nil
}

func generatedSeedFieldMinimum(field definitionmodel.FieldSchema) (float64, bool) {
	if field.Validation.Min != nil {
		return *field.Validation.Min, true
	}
	for _, key := range []string{"min", "minimum"} {
		if value, ok := generatedSeedFloat(field.Config[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func generatedSeedFieldMaximum(field definitionmodel.FieldSchema) (float64, bool) {
	if field.Validation.Max != nil {
		return *field.Validation.Max, true
	}
	for _, key := range []string{"max", "maximum"} {
		if value, ok := generatedSeedFloat(field.Config[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func generatedSeedFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func generatedSeedDate(field definitionmodel.FieldSchema, referenceTime time.Time) time.Time {
	key := generatedSeedSemanticKey(field)
	date := time.Date(referenceTime.Year(), referenceTime.Month(), referenceTime.Day(), 9, 0, 0, 0, time.UTC)
	switch {
	case generatedSeedContainsAny(key, "birth", "birthday"):
		return time.Date(1990, time.January, 15, 9, 0, 0, 0, time.UTC)
	case generatedSeedContainsAny(key, "end", "expiry", "expire", "due", "deadline"):
		return date.AddDate(0, 1, 0)
	case generatedSeedContainsAny(key, "start"):
		return date
	default:
		return date
	}
}

func generatedSeedTextValue(object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema) (string, error) {
	key := generatedSeedSemanticKey(field)
	objectLabel := generatedSeedObjectLabel(object)
	chinese := generatedSeedContainsHan(objectLabel) || generatedSeedContainsHan(field.Name)
	value := "Sample " + objectLabel
	switch {
	case generatedSeedContainsAny(key, "table_no", "table_number"):
		value = "A12"
	case generatedSeedContainsAny(key, "sku"):
		value = generatedSeedCodePrefix(object.Key) + "-001"
	case generatedSeedContainsAny(key, "code", "number", "_no", "identifier", "external_id"):
		value = generatedSeedCodePrefix(object.Key) + "-001"
	case generatedSeedContainsAny(key, "first_name", "given_name"):
		value = "Alex"
	case generatedSeedContainsAny(key, "last_name", "family_name"):
		value = "Chen"
	case generatedSeedContainsAny(key, "contact_name", "person_name", "employee_name", "applicant_name", "customer_name"):
		value = "Alex Chen"
	case generatedSeedContainsAny(key, "company_name", "account_name", "supplier_name", "vendor_name"):
		value = "Acme Trading"
	case generatedSeedContainsAny(key, "name"):
		value = generatedSeedEntityName(object)
	case generatedSeedContainsAny(key, "title", "subject"):
		value = generatedSeedEntityTitle(object)
	case generatedSeedContainsAny(key, "description", "summary", "notes", "remark", "comment"):
		if chinese {
			value = objectLabel + "的初始化示例记录，用于首次打开时展示完整业务关系。"
		} else {
			value = "Initial " + strings.ToLower(objectLabel) + " record showing the configured business relationships."
		}
	case generatedSeedContainsAny(key, "street", "address"):
		value = "100 Century Avenue, Pudong, Shanghai"
	case generatedSeedContainsAny(key, "city"):
		value = "Shanghai"
	case generatedSeedContainsAny(key, "province", "state"):
		value = "Shanghai"
	case generatedSeedContainsAny(key, "country"):
		value = "China"
	case generatedSeedContainsAny(key, "postal", "zip"):
		value = "200120"
	case generatedSeedContainsAny(key, "reason"):
		if chinese {
			value = "首次初始化业务数据"
		} else {
			value = "Initial business setup"
		}
	case generatedSeedContainsAny(key, "type", "category", "kind"):
		value = "standard"
	default:
		fieldLabel := strings.TrimSpace(field.Name)
		if fieldLabel == "" {
			fieldLabel = generatedSeedTitle(field.Key)
		}
		if chinese {
			value = objectLabel + " · " + fieldLabel
		} else {
			value = objectLabel + " " + fieldLabel
		}
	}
	return generatedSeedFitText(field, value)
}

func generatedSeedEntityName(object definitionmodel.ObjectSchema) string {
	key := strings.ToLower(object.Key)
	if generatedSeedContainsHan(generatedSeedObjectLabel(object)) {
		switch {
		case generatedSeedContainsAny(key, "customer", "account", "client", "supplier", "vendor", "company"):
			return "上海远航商贸有限公司"
		case generatedSeedContainsAny(key, "product", "item", "inventory", "stock"):
			return "标准滤芯 A 型"
		case generatedSeedContainsAny(key, "employee", "person", "contact", "applicant", "borrower", "patient"):
			return "陈晨"
		case generatedSeedContainsAny(key, "project"):
			return "华东区上线项目"
		default:
			return "示例" + generatedSeedObjectLabel(object)
		}
	}
	switch {
	case generatedSeedContainsAny(key, "customer", "account", "client", "supplier", "vendor", "company"):
		return "Acme Trading"
	case generatedSeedContainsAny(key, "product", "item", "inventory", "stock"):
		return "Standard Filter Cartridge"
	case generatedSeedContainsAny(key, "employee", "person", "contact", "applicant", "borrower", "patient"):
		return "Alex Chen"
	case generatedSeedContainsAny(key, "project"):
		return "North Region Rollout"
	case generatedSeedContainsAny(key, "order"):
		return "Office Supply Order"
	default:
		return "Sample " + generatedSeedObjectLabel(object)
	}
}

func generatedSeedEntityTitle(object definitionmodel.ObjectSchema) string {
	key := strings.ToLower(object.Key)
	if generatedSeedContainsHan(generatedSeedObjectLabel(object)) {
		switch {
		case generatedSeedContainsAny(key, "opportunity", "deal"):
			return "年度服务扩容"
		case generatedSeedContainsAny(key, "case", "request", "task", "ticket"):
			return "首次服务申请"
		case generatedSeedContainsAny(key, "project"):
			return "华东区上线项目"
		default:
			return "首条" + generatedSeedObjectLabel(object)
		}
	}
	switch {
	case generatedSeedContainsAny(key, "opportunity", "deal"):
		return "Annual Service Expansion"
	case generatedSeedContainsAny(key, "case", "request", "task", "ticket"):
		return "Initial Service Request"
	case generatedSeedContainsAny(key, "project"):
		return "North Region Rollout"
	default:
		return "Initial " + generatedSeedObjectLabel(object)
	}
}

func generatedSeedFitText(field definitionmodel.FieldSchema, preferred string) (string, error) {
	minimum := field.Validation.MinLength
	maximum := field.Validation.MaxLength
	if value, ok := generatedSeedInt(field.Config["min_length"]); ok && value > minimum {
		minimum = value
	}
	if value, ok := generatedSeedInt(field.Config["max_length"]); ok && (maximum == 0 || value < maximum) {
		maximum = value
	}
	if maximum > 0 && minimum > maximum {
		return "", fmt.Errorf("text field %s has minimum length %d above maximum length %d", field.Key, minimum, maximum)
	}
	pattern := strings.TrimSpace(field.Validation.Pattern)
	if configured := strings.TrimSpace(fmt.Sprint(field.Config["pattern"])); configured != "" && configured != "<nil>" {
		pattern = configured
	}
	var expression *regexp.Regexp
	if pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("text field %s has invalid pattern: %w", field.Key, err)
		}
		expression = compiled
	}
	candidates := []string{
		preferred, "sample", "example", "alpha", "A12", "ABC-001", "ITEM-001", "10001",
		"13800138000", "00000000-0000-4000-8000-000000000001", strings.Repeat("a", max(1, minimum)), strings.Repeat("a", 64),
	}
	for _, candidate := range candidates {
		candidate = generatedSeedFitTextLength(candidate, minimum, maximum, pattern)
		if candidate == "" || (expression != nil && !expression.MatchString(candidate)) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("text field %s pattern %q has no safe deterministic baseline value", field.Key, pattern)
}

func generatedSeedFitTextLength(value string, minimum, maximum int, pattern string) string {
	padding := "x"
	if strings.Contains(pattern, "[0-9]") || strings.Contains(pattern, "\\d") {
		padding = "0"
	} else if strings.Contains(pattern, "[A-Z]") {
		padding = "A"
	}
	for len(value) < minimum {
		value += padding
	}
	if maximum > 0 && len(value) > maximum {
		for len(value) > maximum && value != "" {
			_, size := utf8.DecodeLastRuneInString(value)
			value = value[:len(value)-size]
		}
	}
	return value
}

func generatedSeedApplyValidationHints(object definitionmodel.ObjectSchema, data map[string]any) {
	for _, validation := range object.Validations {
		if !recordvalidation.RecordValidationBlocks(validation) {
			continue
		}
		switch strings.TrimSpace(validation.Type) {
		case "boolean_true", "truthy":
			if key := strings.TrimSpace(validation.FieldKey); key != "" {
				data[key] = true
			}
		case "fields_not_equal", "not_equal":
			if len(validation.Fields) < 2 {
				continue
			}
			left, right := validation.Fields[0], validation.Fields[1]
			if fmt.Sprint(data[left]) != "" && fmt.Sprint(data[left]) == fmt.Sprint(data[right]) {
				switch value := data[right].(type) {
				case float64:
					data[right] = value + 1
				case int64:
					data[right] = value + 1
				case string:
					data[right] = value + " 2"
				}
			}
		}
	}
}

func generatedSeedSemanticKey(field definitionmodel.FieldSchema) string {
	return strings.ToLower(strings.TrimSpace(field.Key + " " + field.Name))
}

func generatedSeedContainsAny(value string, candidates ...string) bool {
	value = strings.ToLower(value)
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func generatedSeedContainsHan(value string) bool {
	for _, character := range value {
		if unicode.Is(unicode.Han, character) {
			return true
		}
	}
	return false
}

func generatedSeedObjectLabel(object definitionmodel.ObjectSchema) string {
	if name := strings.TrimSpace(object.Name); name != "" {
		return name
	}
	return generatedSeedTitle(object.Key)
}

func generatedSeedTitle(value string) string {
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.TrimSpace(value)))
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func generatedSeedCodePrefix(value string) string {
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.TrimSpace(value)))
	if len(parts) > 1 {
		var prefix strings.Builder
		for _, part := range parts {
			if part != "" {
				prefix.WriteString(strings.ToUpper(part[:1]))
			}
		}
		return prefix.String()
	}
	prefix := strings.ToUpper(strings.TrimSpace(value))
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	if prefix == "" {
		return "ITEM"
	}
	return prefix
}

func generatedSeedInt(value any) (int, bool) {
	number, ok := generatedSeedFloat(value)
	if !ok || math.Trunc(number) != number {
		return 0, false
	}
	return int(number), true
}

func generatedSeedStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
