package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func decodeBusinessHandlerOutput(payload json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	value, err := consumeBusinessHandlerJSONValue(decoder, true)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("Business Handler output must contain exactly one JSON value")
		}
		return nil, err
	}
	return value.(map[string]any), nil
}

func consumeBusinessHandlerJSONValue(decoder *json.Decoder, requireObject bool) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, structured := token.(json.Delim)
	if requireObject && (!structured || delimiter != '{') {
		return nil, errors.New("Business Handler output must be a JSON object")
	}
	if !structured {
		return token, nil
	}
	if delimiter == '{' {
		output := map[string]any{}
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key := keyToken.(string)
			if seen[key] {
				return nil, fmt.Errorf("Business Handler output contains duplicate field %s", key)
			}
			seen[key] = true
			value, err := consumeBusinessHandlerJSONValue(decoder, false)
			if err != nil {
				return nil, err
			}
			output[key] = value
		}
		if _, err = decoder.Token(); err != nil {
			return nil, err
		}
		return output, nil
	}
	output := []any{}
	for decoder.More() {
		value, err := consumeBusinessHandlerJSONValue(decoder, false)
		if err != nil {
			return nil, err
		}
		output = append(output, value)
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	return output, nil
}
