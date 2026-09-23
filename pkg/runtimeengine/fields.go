package runtimeengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// DecodeFields projects Runtime-owned record fields into a project-owned
// typed struct. The struct controls the projection through ordinary json tags;
// record identity and timestamps remain available on Record itself.
func DecodeFields[T any](record Record) (T, error) {
	var target T
	payload, err := json.Marshal(record.Fields)
	if err == nil {
		err = json.Unmarshal(payload, &target)
	}
	if err != nil {
		return target, NewError(ErrorInternal, "backend.project.record_decode_failed", map[string]string{
			"object": strings.TrimSpace(record.ObjectKey),
			"record": strings.TrimSpace(record.ID),
		}, err)
	}
	return target, nil
}

// EncodeFields converts a typed project value into the map accepted by Engine
// mutations without exposing persistence or transport representations.
func EncodeFields(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, NewError(ErrorInternal, "backend.project.record_encode_failed", nil, err)
	}
	fields := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, NewError(ErrorInternal, "backend.project.record_encode_failed", nil, err)
	}
	if fields == nil {
		return nil, NewError(ErrorInternal, "backend.project.record_encode_failed", nil, errors.New("project fields must encode to a JSON object"))
	}
	return fields, nil
}
