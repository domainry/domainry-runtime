// Package binding owns the value/reference contract shared by metadata
// authoring validators and Runtime definition validators. It deliberately does
// not render values: execution owners remain responsible for rendering, while
// this package proves that every authored reference has a declared producer,
// a stable type, and is available at the consumer.
package binding

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type ValueType string

const (
	TypeUnknown  ValueType = "unknown"
	TypeBoolean  ValueType = "boolean"
	TypeInteger  ValueType = "integer"
	TypeNumber   ValueType = "number"
	TypeText     ValueType = "text"
	TypeDate     ValueType = "date"
	TypeDateTime ValueType = "datetime"
	TypeJSON     ValueType = "json"
	TypeUser     ValueType = "user"
	TypeRelation ValueType = "relation"
)

type Fact struct {
	Reference string
	Type      ValueType
	Producer  string
	// Values is the complete finite value domain when the producer contract
	// publishes one. An empty slice means the value domain is not statically
	// bounded; it does not mean that the producer can only emit an empty value.
	Values []string
}

type Environment map[string]Fact

type Occurrence struct {
	Reference string
	Path      string
	Exact     bool
}

func NewEnvironment(facts ...Fact) Environment {
	result := Environment{}
	for _, fact := range facts {
		result.Add(fact)
	}
	return result
}

func (environment Environment) Add(fact Fact) {
	fact.Reference = strings.TrimSpace(fact.Reference)
	if fact.Reference == "" || !strings.HasPrefix(fact.Reference, "$") {
		return
	}
	fact.Type = NormalizeType(string(fact.Type))
	fact.Values = normalizedValues(fact.Values)
	environment[fact.Reference] = fact
}

func (environment Environment) Clone() Environment {
	result := make(Environment, len(environment))
	for reference, fact := range environment {
		result[reference] = fact
	}
	return result
}

// Intersect returns only identically typed facts available in every incoming
// environment. This is the fail-closed join rule used by graph consumers.
func Intersect(environments ...Environment) Environment {
	if len(environments) == 0 {
		return Environment{}
	}
	result := environments[0].Clone()
	for _, environment := range environments[1:] {
		for reference, fact := range result {
			other, exists := environment[reference]
			if !exists || NormalizeType(string(other.Type)) != NormalizeType(string(fact.Type)) {
				delete(result, reference)
				continue
			}
			fact.Values = joinedValues(fact.Values, other.Values)
			result[reference] = fact
		}
	}
	return result
}

func normalizedValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func joinedValues(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	return normalizedValues(append(append([]string(nil), left...), right...))
}

func (environment Environment) References() []string {
	result := make([]string, 0, len(environment))
	for reference := range environment {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result
}

func NormalizeType(value string) ValueType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bool", "boolean":
		return TypeBoolean
	case "int", "int32", "int64", "integer":
		return TypeInteger
	case "decimal", "float", "float32", "float64", "currency", "number", "percent":
		return TypeNumber
	case "date":
		return TypeDate
	case "datetime", "timestamp":
		return TypeDateTime
	case "user", "identity_user":
		return TypeUser
	case "relation", "record_id":
		return TypeRelation
	case "json", "object", "array":
		return TypeJSON
	case "email", "file", "long_text", "phone", "select", "string", "text", "url":
		return TypeText
	default:
		return TypeUnknown
	}
}

func LiteralType(value any) ValueType {
	switch typed := value.(type) {
	case bool:
		return TypeBoolean
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return TypeInteger
	case float32:
		value := float64(typed)
		if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value {
			return TypeInteger
		}
		return TypeNumber
	case float64:
		if !math.IsNaN(typed) && !math.IsInf(typed, 0) && math.Trunc(typed) == typed {
			return TypeInteger
		}
		return TypeNumber
	case string:
		return TypeText
	case map[string]any, []any:
		return TypeJSON
	default:
		return TypeUnknown
	}
}

// Compatible is intentionally lossless. User/relation values may bind to a
// textual protocol field because Runtime transports their stable IDs as
// strings, but arbitrary text is not promoted into a user/relation value.
func Compatible(source, target ValueType) bool {
	source, target = NormalizeType(string(source)), NormalizeType(string(target))
	if target == TypeUnknown {
		return true
	}
	if source == TypeUnknown {
		return false
	}
	if source == target || target == TypeJSON {
		return true
	}
	if source == TypeInteger && target == TypeNumber {
		return true
	}
	if target == TypeText && (source == TypeUser || source == TypeRelation || source == TypeDate || source == TypeDateTime) {
		return true
	}
	return false
}

// ReferencesInString returns every syntactically complete reference token in
// a value. Dots separate identifier segments; trailing punctuation belongs to
// the surrounding template rather than the reference.
func ReferencesInString(value string) []string {
	result := []string{}
	for offset := 0; offset < len(value); {
		index := strings.IndexByte(value[offset:], '$')
		if index < 0 {
			break
		}
		start := offset + index
		end := start + 1
		for end < len(value) {
			character := value[end]
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '.' {
				end++
				continue
			}
			break
		}
		reference := strings.TrimSuffix(value[start:end], ".")
		if validReference(reference) {
			result = append(result, reference)
		}
		offset = end
	}
	return result
}

func Walk(value any, path string, visit func(Occurrence)) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		for _, reference := range ReferencesInString(typed) {
			visit(Occurrence{Reference: reference, Path: path, Exact: trimmed == reference})
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			Walk(typed[key], path+"/"+escapePointerToken(key), visit)
		}
	case []any:
		for index, item := range typed {
			Walk(item, fmt.Sprintf("%s/%d", path, index), visit)
		}
	}
}

// ValueTypeOf resolves an exact reference to its producer type. A template
// containing one or more references is textual only when every token resolves.
func ValueTypeOf(value any, environment Environment) (ValueType, bool) {
	text, stringValue := value.(string)
	if !stringValue {
		valueType := LiteralType(value)
		return valueType, valueType != TypeUnknown
	}
	references := ReferencesInString(text)
	if len(references) == 0 {
		return TypeText, true
	}
	trimmed := strings.TrimSpace(text)
	if len(references) == 1 && trimmed == references[0] {
		fact, exists := environment[references[0]]
		return fact.Type, exists
	}
	for _, reference := range references {
		if _, exists := environment[reference]; !exists {
			return TypeUnknown, false
		}
	}
	return TypeText, true
}

func validReference(reference string) bool {
	if len(reference) < 2 || reference[0] != '$' || reference[len(reference)-1] == '.' {
		return false
	}
	segments := strings.Split(reference[1:], ".")
	for _, segment := range segments {
		if segment == "" {
			return false
		}
		for _, character := range segment {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' {
				continue
			}
			return false
		}
	}
	return true
}

func escapePointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
