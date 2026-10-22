package workflow

import "github.com/domainry/domainry-foundation/entityid"

func workflowTestAuditEventID(label string) string {
	id, err := entityid.Derive("audevt", "runtime-workflow-persistence-test", label)
	if err != nil {
		panic(err)
	}
	return id
}
