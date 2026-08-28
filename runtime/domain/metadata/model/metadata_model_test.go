package metadatamodel

import "testing"

func TestMetadataPhysicalSchemaMismatchErrorParameters(t *testing.T) {
	err := &MetadataPhysicalSchemaMismatchError{ObjectKey: "order", ColumnKey: "amount", ExpectedType: "decimal", ActualType: "text"}
	params := err.ErrorParams()
	if err.ErrorCode() != "backend.metadata.physical_schema_incompatible" || params["object_key"] != "order" || params["column_key"] != "amount" || params["expected_type"] != "decimal" || params["actual_type"] != "text" {
		t.Fatalf("error=%v params=%#v", err, params)
	}
}

func TestMetadataDefinitionConflictErrorMessage(t *testing.T) {
	if got := (&MetadataDefinitionConflictError{}).Error(); got != "metadata.definition.versionConflict" {
		t.Fatalf("message=%q", got)
	}
}
