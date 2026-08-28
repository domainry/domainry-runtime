package policy

import "strings"

// CapabilityValidationError is the stable coded error returned by capability policies.
type CapabilityValidationError struct {
	Code   string
	Params map[string]string
}

func (e *CapabilityValidationError) Error() string {
	return e.Code
}

func (e *CapabilityValidationError) ErrorCode() string {
	return strings.TrimSpace(e.Code)
}

func (e *CapabilityValidationError) ErrorParams() map[string]string {
	if len(e.Params) == 0 {
		return nil
	}
	params := make(map[string]string, len(e.Params))
	for key, value := range e.Params {
		params[key] = value
	}
	return params
}

func validationError(code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			params[key] = values[index+1]
		}
	}
	if len(params) == 0 {
		params = nil
	}
	return &CapabilityValidationError{Code: strings.TrimSpace(code), Params: params}
}
