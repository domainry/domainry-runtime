package integrationcontract

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestOperationContractSHA256IsStableAcrossProviders(t *testing.T) {
	operation := integrationmodel.ConnectorOperationSchema{Key: "lookup", Method: "GET", ExecutionMode: "sync", SideEffect: "read"}
	first, err := OperationContractSHA256("directory", "primary", operation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OperationContractSHA256("directory", "primary", operation)
	if err != nil {
		t.Fatal(err)
	}
	otherProvider, err := OperationContractSHA256("directory", "secondary", operation)
	if err != nil {
		t.Fatal(err)
	}
	differentOperation, err := OperationContractSHA256("directory", "primary", integrationmodel.ConnectorOperationSchema{
		Key: "search", Method: "GET", ExecutionMode: "sync", SideEffect: "read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != otherProvider || first == differentOperation || len(first) != 64 {
		t.Fatalf("first=%q second=%q otherProvider=%q", first, second, otherProvider)
	}
}
