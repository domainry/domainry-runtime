package projectmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type DecodeError struct {
	Code    string
	Pointer string
	Message string
	Cause   error
}

func (e *DecodeError) Error() string {
	location := e.Pointer
	if location == "" {
		location = "/"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s at %s: %s: %v", e.Code, location, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s at %s: %s", e.Code, location, e.Message)
}

func (e *DecodeError) Unwrap() error { return e.Cause }

func Decode(raw []byte) (Model, error) {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Model{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var model Model
	if err := decoder.Decode(&model); err != nil {
		return Model{}, &DecodeError{Code: "project_model.decode_failed", Message: "project model is not valid closed JSON", Cause: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return Model{}, &DecodeError{Code: "project_model.trailing_value", Message: "project model contains trailing JSON values", Cause: err}
	}
	if strings.TrimSpace(model.SchemaVersion) != CurrentSchemaVersion {
		return Model{}, &DecodeError{Code: "project_model.schema_version_unsupported", Pointer: "/schema_version", Message: fmt.Sprintf("schema_version must be %q", CurrentSchemaVersion)}
	}
	if err := normalizeMapKeys(&model); err != nil {
		return Model{}, err
	}
	if err := Validate(model, nil); err != nil {
		return Model{}, err
	}
	return model, nil
}

func normalizeMapKeys(model *Model) error {
	for key, object := range model.Objects {
		key = strings.TrimSpace(key)
		if object.Key != "" && strings.TrimSpace(object.Key) != key {
			return &DecodeError{Code: "project_model.object_key_mismatch", Pointer: "/objects/" + pointerToken(key) + "/key", Message: "object key must match its map key"}
		}
		object.Key = key
		for fieldKey, field := range object.Fields {
			fieldKey = strings.TrimSpace(fieldKey)
			if field.Key != "" && strings.TrimSpace(field.Key) != fieldKey {
				return &DecodeError{Code: "project_model.field_key_mismatch", Pointer: "/objects/" + pointerToken(key) + "/fields/" + pointerToken(fieldKey) + "/key", Message: "field key must match its map key"}
			}
			field.Key = fieldKey
			field.Name = fieldKey
			object.Fields[fieldKey] = field
		}
		for constraintKey, constraint := range object.UniqueConstraints {
			constraintKey = strings.TrimSpace(constraintKey)
			if constraint.Key != "" && strings.TrimSpace(constraint.Key) != constraintKey {
				return &DecodeError{Code: "project_model.constraint_key_mismatch", Pointer: "/objects/" + pointerToken(key) + "/unique_constraints/" + pointerToken(constraintKey) + "/key", Message: "constraint key must match its map key"}
			}
			constraint.Key = constraintKey
			object.UniqueConstraints[constraintKey] = constraint
		}
		model.Objects[key] = object
	}
	for key, role := range model.Roles {
		key = strings.TrimSpace(key)
		if role.Key != "" && strings.TrimSpace(role.Key) != key {
			return &DecodeError{Code: "project_model.role_key_mismatch", Pointer: "/roles/" + pointerToken(key) + "/key", Message: "role key must match its map key"}
		}
		role.Key = key
		model.Roles[key] = role
	}
	for key, binding := range model.IdentityProfiles {
		key = strings.TrimSpace(key)
		if binding.BusinessIdentity.Key != "" && strings.TrimSpace(binding.BusinessIdentity.Key) != key {
			return &DecodeError{Code: "project_model.identity_profile_key_mismatch", Pointer: "/identity_profiles/" + pointerToken(key) + "/business_identity/key", Message: "business identity key must match its map key"}
		}
		binding.BusinessIdentity.Key = key
		model.IdentityProfiles[key] = binding
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, ""); err != nil {
		return err
	}
	if token, err := decoder.Token(); err == nil {
		return &DecodeError{Code: "project_model.trailing_value", Message: fmt.Sprintf("unexpected trailing token %v", token)}
	} else if !errors.Is(err, io.EOF) {
		return &DecodeError{Code: "project_model.decode_failed", Message: "project model JSON is invalid", Cause: err}
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, pointer string) error {
	token, err := decoder.Token()
	if err != nil {
		return &DecodeError{Code: "project_model.decode_failed", Pointer: pointer, Message: "project model JSON is invalid", Cause: err}
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return &DecodeError{Code: "project_model.decode_failed", Pointer: pointer, Message: "object key is invalid", Cause: err}
			}
			key, ok := keyToken.(string)
			if !ok {
				return &DecodeError{Code: "project_model.decode_failed", Pointer: pointer, Message: "object key must be a string"}
			}
			child := pointer + "/" + pointerToken(key)
			if seen[key] {
				return &DecodeError{Code: "project_model.duplicate_key", Pointer: child, Message: fmt.Sprintf("duplicate JSON key %q", key)}
			}
			seen[key] = true
			if err := consumeJSONValue(decoder, child); err != nil {
				return err
			}
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := consumeJSONValue(decoder, fmt.Sprintf("%s/%d", pointer, index)); err != nil {
				return err
			}
		}
	default:
		return &DecodeError{Code: "project_model.decode_failed", Pointer: pointer, Message: fmt.Sprintf("unexpected delimiter %q", delimiter)}
	}
	if _, err := decoder.Token(); err != nil {
		return &DecodeError{Code: "project_model.decode_failed", Pointer: pointer, Message: "composite JSON value is not closed", Cause: err}
	}
	return nil
}

func pointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
