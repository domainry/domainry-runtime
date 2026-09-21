package recordmodel

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordFileReferenceNormalizesClosedSingleAndListContracts(t *testing.T) {
	reference := map[string]any{
		"file_id": " file-1 ", "filename": "asset.pdf", "content_type": "APPLICATION/PDF", "size": float64(7),
		"content_sha256": strings.Repeat("A", 64), "scan_receipt": " receipt ",
	}
	file := definitionmodel.FieldSchema{Key: "attachment", Type: RecordFileFieldType, Config: map[string]any{
		"allowed_mime_types": []any{"application/pdf"}, "max_size_bytes": float64(10), "scan_required": true,
	}}
	normalized, err := RecordNormalizeFileFieldValue(file, reference)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"file_id": "file-1", "filename": "asset.pdf", "content_type": "application/pdf", "size": int64(7), "content_sha256": strings.Repeat("a", 64), "scan_receipt": "receipt"}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("normalized=%#v want=%#v", normalized, want)
	}

	list := definitionmodel.FieldSchema{Key: "attachments", Type: RecordFileListFieldType, Config: map[string]any{"max_files": 1}}
	if _, err := RecordNormalizeFileFieldValue(list, []any{reference, reference}); fileErrorCode(err) != "backend.validation.file_count" {
		t.Fatalf("count error=%v", err)
	}
	list.Config["max_files"] = 2
	if _, err := RecordNormalizeFileFieldValue(list, []any{reference, reference}); fileErrorCode(err) != "backend.validation.file_duplicate" {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestRecordFileReferenceRejectsUnknownFieldsMIMEAndInvalidPolicy(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "attachment", Type: RecordFileFieldType, Config: map[string]any{"allowed_mime_types": []string{"application/pdf"}}}
	valid := map[string]any{"file_id": "file-1", "filename": "asset.pdf", "content_type": "text/plain", "size": 7, "content_sha256": strings.Repeat("a", 64), "scan_receipt": "receipt"}
	if _, err := RecordNormalizeFileFieldValue(field, valid); fileErrorCode(err) != "backend.validation.file_mime_type" {
		t.Fatalf("mime error=%v", err)
	}
	valid["content_type"] = "application/pdf"
	valid["url"] = "/uploads/file-1"
	if _, err := RecordNormalizeFileFieldValue(field, valid); fileErrorCode(err) != "backend.validation.file_reference" {
		t.Fatalf("closed reference error=%v", err)
	}
	for _, invalid := range []definitionmodel.FieldSchema{
		{Key: "single", Type: RecordFileFieldType, Config: map[string]any{"max_files": 2}},
		{Key: "list", Type: RecordFileListFieldType, Config: map[string]any{"max_files": 101}},
		{Key: "file", Type: RecordFileFieldType, Unique: true},
		{Key: "file", Type: RecordFileFieldType, DefaultValue: map[string]any{"file_id": "shared"}},
		{Key: "file", Type: RecordFileFieldType, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: definitionmodel.FieldUpgradeBackfill, BackfillValue: map[string]any{"file_id": "shared"}}},
		{Key: "file", Type: RecordFileFieldType, Config: map[string]any{"indexed": "false"}},
	} {
		if _, err := RecordFileFieldPolicyFor(invalid); err == nil {
			t.Fatalf("invalid policy passed: %#v", invalid)
		}
	}
}

func TestRecordFileReferenceDatabaseRoundTrip(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "attachments", Type: RecordFileListFieldType, Config: map[string]any{"scan_required": false}}
	value := []any{map[string]any{"file_id": "file-1", "filename": "asset.txt", "content_type": "text/plain", "size": int64(7), "content_sha256": strings.Repeat("a", 64)}}
	encoded, err := RecordEncodeFileFieldValue(field, value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := RecordDecodeFileFieldValue(field, encoded)
	if err != nil || !reflect.DeepEqual(decoded, value) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	decoded, err = RecordDecodeFileFieldValue(field, []byte(encoded))
	if err != nil || !reflect.DeepEqual(decoded, value) {
		t.Fatalf("byte decoded=%#v err=%v", decoded, err)
	}
	if _, err := RecordDecodeFileFieldValue(field, encoded+` {}`); fileErrorCode(err) != "backend.validation.file_reference" {
		t.Fatalf("trailing JSON err=%v", err)
	}
}

func fileErrorCode(err error) string {
	var contract *RecordFileContractError
	if errors.As(err, &contract) {
		return contract.Code
	}
	return ""
}
