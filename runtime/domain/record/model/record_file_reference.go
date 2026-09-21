package recordmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

const (
	RecordFileFieldType     = "file"
	RecordFileListFieldType = "file_list"

	RecordFileDefaultMaxSizeBytes int64 = 5 << 20
	RecordFileDefaultMaxFiles           = 10
	RecordFileMaximumFiles              = 100
)

var recordFileSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RecordFileReference is the one canonical value stored by file and file_list
// fields. Physical storage locations and download URLs are deliberately absent:
// they are resolved from the immutable Runtime-owned file identity.
type RecordFileReference struct {
	FileID        string `json:"file_id"`
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type"`
	Size          int64  `json:"size"`
	ContentSHA256 string `json:"content_sha256"`
	ScanReceipt   string `json:"scan_receipt,omitempty"`
}

type RecordFileFieldPolicy struct {
	AllowedMIMETypes []string
	MaxSizeBytes     int64
	MaxFiles         int
	ScanRequired     bool
}

type RecordFileContractError struct {
	Code   string
	Detail string
}

func (e *RecordFileContractError) Error() string {
	if strings.TrimSpace(e.Detail) == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func RecordFileFieldPolicyFor(field definitionmodel.FieldSchema) (RecordFileFieldPolicy, error) {
	kind := strings.TrimSpace(field.Type)
	if kind != RecordFileFieldType && kind != RecordFileListFieldType {
		return RecordFileFieldPolicy{}, fileContractError("backend.file.field_type_invalid", kind)
	}
	policy := RecordFileFieldPolicy{MaxSizeBytes: RecordFileDefaultMaxSizeBytes, MaxFiles: 1, ScanRequired: true}
	if kind == RecordFileListFieldType {
		policy.MaxFiles = RecordFileDefaultMaxFiles
	}
	if field.Unique {
		return RecordFileFieldPolicy{}, fileContractError("backend.file.unique_unsupported", field.Key)
	}
	if field.Default != nil || field.DefaultValue != nil || field.Upgrade != nil && strings.TrimSpace(field.Upgrade.ExistingRows) == definitionmodel.FieldUpgradeBackfill {
		return RecordFileFieldPolicy{}, fileContractError("backend.file.default_unsupported", field.Key)
	}
	if field.Config == nil {
		return policy, nil
	}
	if raw, found := field.Config["indexed"]; found {
		indexed, ok := raw.(bool)
		if !ok {
			return RecordFileFieldPolicy{}, fileContractError("backend.structured.indexed_invalid", field.Key)
		}
		if indexed {
			return RecordFileFieldPolicy{}, fileContractError("backend.file.index_unsupported", field.Key)
		}
	}
	if raw, found := field.Config["max_size_bytes"]; found {
		value, ok := recordFileInt64(raw)
		if !ok || value < 1 || value > RecordFileDefaultMaxSizeBytes {
			return RecordFileFieldPolicy{}, fileContractError("backend.file.max_size_invalid", fmt.Sprint(raw))
		}
		policy.MaxSizeBytes = value
	}
	if raw, found := field.Config["max_files"]; found {
		value, ok := recordFileInt64(raw)
		if !ok || value < 1 || value > RecordFileMaximumFiles || kind == RecordFileFieldType && value != 1 {
			return RecordFileFieldPolicy{}, fileContractError("backend.file.max_files_invalid", fmt.Sprint(raw))
		}
		policy.MaxFiles = int(value)
	}
	if raw, found := field.Config["scan_required"]; found {
		value, ok := raw.(bool)
		if !ok {
			return RecordFileFieldPolicy{}, fileContractError("backend.file.scan_required_invalid", fmt.Sprint(raw))
		}
		policy.ScanRequired = value
	}
	if raw, found := field.Config["allowed_mime_types"]; found {
		values, ok := recordFileStringSlice(raw)
		if !ok || len(values) == 0 {
			return RecordFileFieldPolicy{}, fileContractError("backend.file.allowed_mime_types_invalid", field.Key)
		}
		seen := map[string]bool{}
		for _, rawValue := range values {
			value := strings.ToLower(strings.TrimSpace(rawValue))
			parsed, parameters, err := mime.ParseMediaType(value)
			if err != nil || parsed != value || len(parameters) != 0 || seen[value] {
				return RecordFileFieldPolicy{}, fileContractError("backend.file.allowed_mime_types_invalid", rawValue)
			}
			seen[value] = true
			policy.AllowedMIMETypes = append(policy.AllowedMIMETypes, value)
		}
		sort.Strings(policy.AllowedMIMETypes)
	}
	return policy, nil
}

func (p RecordFileFieldPolicy) AllowsContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(p.AllowedMIMETypes) == 0 {
		return true
	}
	for _, allowed := range p.AllowedMIMETypes {
		if value == allowed {
			return true
		}
	}
	return false
}

func RecordNormalizeFileFieldValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	policy, err := RecordFileFieldPolicyFor(field)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(field.Type) == RecordFileFieldType {
		reference, err := recordDecodeFileReference(value, policy)
		if err != nil {
			return nil, err
		}
		return recordFileReferenceMap(reference), nil
	}
	items, ok := recordFileSlice(value)
	if !ok {
		return nil, fileContractError("backend.validation.file_list", field.Key)
	}
	if len(items) > policy.MaxFiles {
		return nil, fileContractError("backend.validation.file_count", field.Key)
	}
	result := make([]any, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		reference, err := recordDecodeFileReference(item, policy)
		if err != nil {
			return nil, err
		}
		if seen[reference.FileID] {
			return nil, fileContractError("backend.validation.file_duplicate", field.Key)
		}
		seen[reference.FileID] = true
		result = append(result, recordFileReferenceMap(reference))
	}
	return result, nil
}

func RecordFileReferences(field definitionmodel.FieldSchema, value any) ([]RecordFileReference, error) {
	normalized, err := RecordNormalizeFileFieldValue(field, value)
	if err != nil || normalized == nil {
		return nil, err
	}
	values := []any{normalized}
	if strings.TrimSpace(field.Type) == RecordFileListFieldType {
		values, _ = normalized.([]any)
	}
	result := make([]RecordFileReference, 0, len(values))
	for _, item := range values {
		encoded, _ := json.Marshal(item)
		var reference RecordFileReference
		if err := json.Unmarshal(encoded, &reference); err != nil {
			return nil, fileContractError("backend.validation.file_reference", field.Key)
		}
		result = append(result, reference)
	}
	return result, nil
}

func RecordEncodeFileFieldValue(field definitionmodel.FieldSchema, value any) (string, error) {
	normalized, err := RecordNormalizeFileFieldValue(field, value)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func RecordDecodeFileFieldValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = append([]byte(nil), typed...)
	case string:
		raw = []byte(typed)
	default:
		return RecordNormalizeFileFieldValue(field, value)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fileContractError("backend.validation.file_reference", field.Key)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fileContractError("backend.validation.file_reference", field.Key)
	}
	return RecordNormalizeFileFieldValue(field, decoded)
}

func recordDecodeFileReference(value any, policy RecordFileFieldPolicy) (RecordFileReference, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return RecordFileReference{}, fileContractError("backend.validation.file_reference", "encode")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var reference RecordFileReference
	if err := decoder.Decode(&reference); err != nil {
		return RecordFileReference{}, fileContractError("backend.validation.file_reference", err.Error())
	}
	reference.FileID = strings.TrimSpace(reference.FileID)
	reference.Filename = strings.TrimSpace(reference.Filename)
	reference.ContentType = strings.ToLower(strings.TrimSpace(reference.ContentType))
	reference.ContentSHA256 = strings.ToLower(strings.TrimSpace(reference.ContentSHA256))
	reference.ScanReceipt = strings.TrimSpace(reference.ScanReceipt)
	if reference.FileID == "" || reference.Filename == "" || filepath.Base(reference.Filename) != reference.Filename || reference.ContentType == "" || reference.Size < 1 || reference.Size > policy.MaxSizeBytes || !recordFileSHA256Pattern.MatchString(reference.ContentSHA256) || policy.ScanRequired && reference.ScanReceipt == "" {
		return RecordFileReference{}, fileContractError("backend.validation.file_reference", reference.FileID)
	}
	if !policy.AllowsContentType(reference.ContentType) {
		return RecordFileReference{}, fileContractError("backend.validation.file_mime_type", reference.ContentType)
	}
	return reference, nil
}

func recordFileReferenceMap(reference RecordFileReference) map[string]any {
	result := map[string]any{
		"file_id": reference.FileID, "filename": reference.Filename, "content_type": reference.ContentType,
		"size": reference.Size, "content_sha256": reference.ContentSHA256,
	}
	if reference.ScanReceipt != "" {
		result["scan_receipt"] = reference.ScanReceipt
	}
	return result
}

func recordFileSlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []RecordFileReference:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = typed[index]
		}
		return result, true
	default:
		return nil, false
	}
}

func recordFileStringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func recordFileInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func fileContractError(code, detail string) error {
	return &RecordFileContractError{Code: code, Detail: strings.TrimSpace(detail)}
}
