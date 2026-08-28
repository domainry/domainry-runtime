package upload

import (
	"fmt"
	"path/filepath"
	"strings"
)

func uploadFileMatchesRecord(filename string, value any) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return false
	}
	text = strings.TrimPrefix(text, "file://")
	text = strings.Split(text, "?")[0]
	return filepath.Base(text) == filename
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
