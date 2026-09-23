package runtimeext

import (
	"reflect"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

var typedPayloadTimeType = reflect.TypeFor[time.Time]()

// TypedPayloadFields derives Runtime's closed JSON payload tree from a
// source-owned typed Handler input. Projects call it explicitly from their Go
// Handler registry; model.json never owns or duplicates this behavior.
func TypedPayloadFields[Input any]() []definitionmodel.ActionPayloadField {
	return typedPayloadStructFields(reflect.TypeFor[Input]())
}

func typedPayloadStructFields(input reflect.Type) []definitionmodel.ActionPayloadField {
	input = typedPayloadDereference(input)
	if input.Kind() != reflect.Struct {
		return []definitionmodel.ActionPayloadField{}
	}
	fields := make([]definitionmodel.ActionPayloadField, 0, input.NumField())
	for index := 0; index < input.NumField(); index++ {
		goField := input.Field(index)
		if !goField.IsExported() {
			continue
		}
		name, options := typedPayloadJSONTag(goField.Tag.Get("json"))
		if name == "-" {
			continue
		}
		if name == "" {
			name = goField.Name
		}
		field := typedPayloadField(name, goField.Type)
		field.Required = !options["omitempty"]
		fields = append(fields, field)
	}
	return fields
}

func typedPayloadField(name string, input reflect.Type) definitionmodel.ActionPayloadField {
	input = typedPayloadDereference(input)
	field := definitionmodel.ActionPayloadField{Key: name, Name: name}
	if input.Kind() == reflect.Slice || input.Kind() == reflect.Array {
		field.Repeated = true
		input = typedPayloadDereference(input.Elem())
	}
	if input.Kind() == reflect.Struct && input != typedPayloadTimeType {
		field.Type = definitionmodel.ActionPayloadTypeObject
		field.Fields = typedPayloadStructFields(input)
		return field
	}
	switch input.Kind() {
	case reflect.Bool:
		field.Type = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.Type = "integer"
	case reflect.Float32, reflect.Float64:
		field.Type = "number"
	default:
		field.Type = "text"
	}
	return field
}

func typedPayloadDereference(value reflect.Type) reflect.Type {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func typedPayloadJSONTag(tag string) (string, map[string]bool) {
	parts := strings.Split(tag, ",")
	options := make(map[string]bool, len(parts))
	for _, option := range parts[1:] {
		options[strings.TrimSpace(option)] = true
	}
	return strings.TrimSpace(parts[0]), options
}
