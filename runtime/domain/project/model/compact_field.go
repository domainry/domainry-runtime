package projectmodel

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// UnmarshalJSON accepts the source model's compact field declaration. The
// expanded Field value exists only inside Runtime after decoding.
func (field *Field) UnmarshalJSON(raw []byte) error {
	var declaration string
	if err := json.Unmarshal(raw, &declaration); err != nil {
		return fmt.Errorf("field declaration must be a compact string: %w", err)
	}
	parsed, err := parseFieldDeclaration(declaration)
	if err != nil {
		return err
	}
	*field = parsed
	return nil
}

func parseFieldDeclaration(declaration string) (Field, error) {
	parts := strings.Split(declaration, ";")
	head := parts[0]
	if head == "" || strings.TrimSpace(declaration) != declaration {
		return Field{}, fmt.Errorf("invalid compact field declaration %q", declaration)
	}
	defaultText, hasDefault := "", false
	if index := strings.IndexByte(head, '='); index >= 0 {
		defaultText, hasDefault = head[index+1:], true
		head = head[:index]
		if defaultText == "" {
			return Field{}, fmt.Errorf("field default must not be empty")
		}
	}
	result := Field{}
	if strings.HasPrefix(head, "relation") {
		rest := strings.TrimPrefix(head, "relation")
		if strings.HasPrefix(rest, "!") {
			result.Required = true
			rest = rest[1:]
		}
		if !strings.HasPrefix(rest, "->") || !stableKeyPattern.MatchString(rest[2:]) {
			return Field{}, fmt.Errorf("relation field requires one stable ->target")
		}
		result.Type = "relation"
		result.Relation = &Relation{TargetObjectKey: rest[2:]}
	} else if strings.HasPrefix(head, "select") {
		rest := strings.TrimPrefix(head, "select")
		if strings.HasPrefix(rest, "!") {
			result.Required = true
			rest = rest[1:]
		}
		if len(rest) < 3 || rest[0] != '[' || rest[len(rest)-1] != ']' {
			return Field{}, fmt.Errorf("select field requires [value|value] options")
		}
		result.Type = "select"
		result.Validation.Options = strings.Split(rest[1:len(rest)-1], "|")
		seen := map[string]bool{}
		for _, option := range result.Validation.Options {
			if !stableKeyPattern.MatchString(option) || seen[option] {
				return Field{}, fmt.Errorf("select options must be unique stable keys")
			}
			seen[option] = true
		}
	} else {
		result.Type = strings.TrimSuffix(head, "!")
		result.Required = strings.HasSuffix(head, "!")
		if !stableKeyPattern.MatchString(result.Type) || result.Type == "relation" || result.Type == "select" {
			return Field{}, fmt.Errorf("invalid field type %q", head)
		}
	}
	if hasDefault {
		value, err := compactFieldDefault(result.Type, defaultText)
		if err != nil {
			return Field{}, err
		}
		result.Default = value
		if result.Type == "select" {
			found := false
			for _, option := range result.Validation.Options {
				if option == defaultText {
					found = true
				}
			}
			if !found {
				return Field{}, fmt.Errorf("select default %q is not an option", defaultText)
			}
		}
	}
	seenAttributes := map[string]bool{}
	for _, attribute := range parts[1:] {
		key, value, hasValue := strings.Cut(attribute, "=")
		if key == "" || seenAttributes[key] {
			return Field{}, fmt.Errorf("empty or duplicate field attribute %q", key)
		}
		seenAttributes[key] = true
		switch key {
		case "unique":
			if hasValue {
				return Field{}, fmt.Errorf("unique does not take a value")
			}
			result.Unique = true
		case "sensitive":
			if hasValue {
				return Field{}, fmt.Errorf("sensitive does not take a value")
			}
			result.Sensitive = true
		case "min_length", "max_length":
			number, err := strconv.Atoi(value)
			if !hasValue || err != nil || number <= 0 {
				return Field{}, fmt.Errorf("%s requires a positive integer", key)
			}
			if key == "min_length" {
				result.Validation.MinLength = number
			} else {
				result.Validation.MaxLength = number
			}
		case "min", "max":
			number, err := strconv.ParseFloat(value, 64)
			if !hasValue || err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return Field{}, fmt.Errorf("%s requires a number", key)
			}
			if key == "min" {
				result.Validation.Min = &number
			} else {
				result.Validation.Max = &number
			}
		case "pattern":
			if !hasValue || value == "" {
				return Field{}, fmt.Errorf("pattern requires a value")
			}
			result.Validation.Pattern = value
		default:
			return Field{}, fmt.Errorf("unsupported field attribute %q", key)
		}
	}
	return result, nil
}

func compactFieldDefault(fieldType, value string) (json.RawMessage, error) {
	switch fieldType {
	case "integer":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return nil, fmt.Errorf("invalid integer default %q", value)
		}
		return json.RawMessage(value), nil
	case "number", "decimal", "percent":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || !json.Valid([]byte(value)) {
			return nil, fmt.Errorf("invalid %s default %q", fieldType, value)
		}
		return json.RawMessage(value), nil
	case "boolean":
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("invalid boolean default %q", value)
		}
		return json.RawMessage(value), nil
	default:
		encoded, err := json.Marshal(value)
		return encoded, err
	}
}
