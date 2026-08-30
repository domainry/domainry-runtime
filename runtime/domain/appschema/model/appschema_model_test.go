package appschemamodel

import "testing"

func TestMetadataPhysicalSchemaMismatchErrorParameters(t *testing.T) {
	err := &ApplicationSchemaPhysicalSchemaMismatchError{ObjectKey: "order", ColumnKey: "amount", ExpectedType: "decimal", ActualType: "text"}
	params := err.ErrorParams()
	if err.ErrorCode() != "backend.metadata.physical_schema_incompatible" || params["object_key"] != "order" || params["column_key"] != "amount" || params["expected_type"] != "decimal" || params["actual_type"] != "text" {
		t.Fatalf("error=%v params=%#v", err, params)
	}
}
