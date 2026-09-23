package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	OperationsMaximumInlineResultBytes   = 16 * 1024
	OperationsMaximumArtifactResultBytes = 10 * 1024 * 1024
)

var operationsSensitiveResultKeys = map[string]bool{
	"access_token":   true,
	"api_key":        true,
	"authorization":  true,
	"bearer_token":   true,
	"client_secret":  true,
	"content_base64": true,
	"cookie":         true,
	"credential":     true,
	"credentials":    true,
	"password":       true,
	"password_hash":  true,
	"payload":        true,
	"private_key":    true,
	"raw_content":    true,
	"refresh_token":  true,
	"request_body":   true,
	"response_body":  true,
	"secret":         true,
	"set_cookie":     true,
}

func OperationsRedactResult(result json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(result)) == 0 {
		return nil, nil
	}
	if !json.Valid(result) {
		return nil, fmt.Errorf("operation result is invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("operation result is invalid JSON: %w", err)
	}
	redacted, err := json.Marshal(redactOperationsResultValue(value))
	if err != nil {
		return nil, fmt.Errorf("encode redacted operation result: %w", err)
	}
	return redacted, nil
}

func redactOperationsResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if operationsResultKeySensitive(key) {
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = redactOperationsResultValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = redactOperationsResultValue(typed[index])
		}
		return result
	default:
		return value
	}
}

func operationsResultKeySensitive(key string) bool {
	key = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(key)))
	if operationsSensitiveResultKeys[key] {
		return true
	}
	for _, suffix := range []string{"_password", "_secret", "_credential", "_credentials", "_private_key", "_access_token", "_refresh_token"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}
