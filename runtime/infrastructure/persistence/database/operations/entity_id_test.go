package operations

import "github.com/domainry/domainry-foundation/entityid"

func persistenceOperationsTestID(prefix, label string) string {
	id, err := entityid.Derive(prefix, "runtime-operations-persistence-test", label)
	if err != nil {
		panic(err)
	}
	return id
}
