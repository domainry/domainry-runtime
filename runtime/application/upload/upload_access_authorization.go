package upload

import (
	"fmt"
	"strings"
)

func uploadFileMatchesRecord(filename string, value any) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return false
	}
	return text == "/uploads/"+filename || text == filename
}

func uploadRecordBoolean(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}
