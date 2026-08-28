package validation

import (
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
)

func validationError(code string, params ...string) error {
	code = strings.TrimSpace(code)
	if !isI18nErrorCode(code) {
		code = "backend.validation.invalid"
	}
	return &apperror.CodedError{Code: code, Params: validationParams(params...)}
}

func validationParams(values ...string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		key := strings.TrimSpace(values[index])
		if key == "" {
			continue
		}
		out[key] = values[index+1]
	}
	return out
}

func validationCode(message string, fallback string) string {
	message = strings.TrimSpace(message)
	if isI18nErrorCode(message) {
		return message
	}
	if isI18nErrorCode(fallback) {
		return fallback
	}
	return "backend.validation.invalid"
}

func isI18nErrorCode(code string) bool {
	if !strings.Contains(code, ".") || strings.ContainsAny(code, " \t\n\r") {
		return false
	}
	for _, char := range code {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}
