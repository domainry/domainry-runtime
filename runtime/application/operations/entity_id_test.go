package operations

import "github.com/domainry/domainry-foundation/entityid"

func operationsTestOperationID(label string) string {
	id, err := entityid.Derive("op", "runtime.operations.injected-id", label)
	if err != nil {
		panic(err)
	}
	return id
}

func databaseRetirementTestOperationID(label string) string {
	id, err := entityid.Derive("op", "runtime.database-retirement.injected-id", label)
	if err != nil {
		panic(err)
	}
	return id
}
