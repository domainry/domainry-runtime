package operations

import "github.com/domainry/domainry-foundation/entityid"

func operationsHTTPTestOperationID(label string) string {
	id, err := entityid.Derive("op", "runtime.operations.injected-id", label)
	if err != nil {
		panic(err)
	}
	return id
}
