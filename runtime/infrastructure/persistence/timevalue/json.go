package timevalue

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var jsonTimeType = reflect.TypeOf(time.Time{})
var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
var textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()

const timestampLayout = "2006-01-02T15:04:05.000000000Z"

func MarshalJSON(value any) ([]byte, error) {
	return marshalJSON(value, false)
}

func MarshalJSONIndent(value any) ([]byte, error) {
	return marshalJSON(value, true)
}

func marshalJSON(value any, indent bool) ([]byte, error) {
	// Project declared fields before serialization. Dynamic maps and raw JSON
	// carry their producer's values unchanged.
	projected, err := encodeJSONTimes(reflect.ValueOf(value), "")
	if err != nil {
		return nil, err
	}
	if indent {
		return json.MarshalIndent(projected, "", "  ")
	}
	return json.Marshal(projected)
}

func UnmarshalJSON(raw []byte, destination any) error {
	target := reflect.TypeOf(destination)
	if target == nil || target.Kind() != reflect.Pointer {
		return fmt.Errorf("time JSON destination must be a pointer")
	}
	document, err := jsonDocument(raw)
	if err != nil {
		return err
	}
	document, err = decodeJSONTimes(target.Elem(), document, "")
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, destination)
}

func jsonDocument(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	err := decoder.Decode(&document)
	return document, err
}

func encodeJSONTimes(value reflect.Value, name string) (any, error) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil, nil
		}
		if value.Kind() == reflect.Pointer && value.Type().Elem() != jsonTimeType &&
			(value.Type().Implements(jsonMarshalerType) || value.Type().Implements(textMarshalerType)) {
			return value.Interface(), nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, nil
	}
	if value.Type() == jsonTimeType {
		instant := value.Interface().(time.Time)
		if instant.IsZero() {
			return int64(0), nil
		}
		return instant.UTC().UnixMilli(), nil
	}
	if instantField(name) && value.Kind() == reflect.String {
		text := strings.TrimSpace(value.String())
		if text == "" {
			return int64(0), nil
		}
		instant, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, fmt.Errorf("stored time field %s is invalid: %w", name, err)
		}
		return instant.UTC().UnixMilli(), nil
	}
	if value.Type().Implements(jsonMarshalerType) || value.Type().Implements(textMarshalerType) {
		return value.Interface(), nil
	}
	switch value.Kind() {
	case reflect.Struct:
		object := make(map[string]any)
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				normalized, err := encodeJSONTimes(value.Field(index), "")
				if err != nil {
					return nil, err
				}
				if embedded, ok := normalized.(map[string]any); ok {
					for key, child := range embedded {
						object[key] = child
					}
				}
				continue
			}
			fieldName, included := jsonFieldName(field)
			if !included || jsonOmitEmpty(field, value.Field(index)) {
				continue
			}
			normalized, err := encodeJSONTimes(value.Field(index), fieldName)
			if err != nil {
				return nil, err
			}
			object[fieldName] = normalized
		}
		return object, nil
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return value.Interface(), nil
		}
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil, nil
		}
		items := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			normalized, err := encodeJSONTimes(value.Index(index), "")
			if err != nil {
				return nil, err
			}
			items[index] = normalized
		}
		return items, nil
	default:
		return value.Interface(), nil
	}
}

func jsonOmitEmpty(field reflect.StructField, value reflect.Value) bool {
	for _, option := range strings.Split(field.Tag.Get("json"), ",")[1:] {
		if option == "omitzero" && value.IsZero() {
			return true
		}
		if option == "omitempty" {
			switch value.Kind() {
			case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
				if value.Len() == 0 {
					return true
				}
			case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
				if value.IsZero() {
					return true
				}
			}
		}
	}
	return false
}

func decodeJSONTimes(target reflect.Type, document any, name string) (any, error) {
	for target.Kind() == reflect.Pointer {
		if document == nil {
			return nil, nil
		}
		target = target.Elem()
	}
	if target == jsonTimeType {
		millis, err := jsonMillis(document, name)
		if err != nil {
			return nil, err
		}
		if millis == 0 {
			return "0001-01-01T00:00:00Z", nil
		}
		return time.UnixMilli(millis).UTC().Format(timestampLayout), nil
	}
	if instantField(name) && target.Kind() == reflect.String {
		if document == nil {
			return "", nil
		}
		millis, err := jsonMillis(document, name)
		if err != nil {
			return nil, err
		}
		if millis == 0 {
			return "", nil
		}
		return time.UnixMilli(millis).UTC().Format(timestampLayout), nil
	}
	switch target.Kind() {
	case reflect.Struct:
		object, ok := document.(map[string]any)
		if !ok {
			return document, nil
		}
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				normalized, err := decodeJSONTimes(field.Type, object, "")
				if err != nil {
					return nil, err
				}
				object, _ = normalized.(map[string]any)
				continue
			}
			fieldName, included := jsonFieldName(field)
			child, exists := object[fieldName]
			if !included || !exists {
				continue
			}
			normalized, err := decodeJSONTimes(field.Type, child, fieldName)
			if err != nil {
				return nil, fmt.Errorf("decode stored JSON field %s: %w", fieldName, err)
			}
			object[fieldName] = normalized
		}
		return object, nil
	case reflect.Slice, reflect.Array:
		if target.Elem().Kind() == reflect.Uint8 {
			return document, nil
		}
		items, ok := document.([]any)
		if !ok {
			return document, nil
		}
		for index := range items {
			normalized, err := decodeJSONTimes(target.Elem(), items[index], "")
			if err != nil {
				return nil, err
			}
			items[index] = normalized
		}
		return items, nil
	default:
		return document, nil
	}
}

func jsonMillis(document any, name string) (int64, error) {
	number, ok := document.(json.Number)
	if !ok {
		return 0, fmt.Errorf("stored time field %s must be a Unix-millisecond number", name)
	}
	millis, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("stored time field %s must be an integer: %w", name, err)
	}
	return millis, nil
}

func instantField(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	return strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "_timestamp") || name == "timestamp" || name == "scheduled_for" || name == "not_before" || name == "lease_until" || name == "deliver_after"
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		name = field.Name
	}
	return name, true
}
