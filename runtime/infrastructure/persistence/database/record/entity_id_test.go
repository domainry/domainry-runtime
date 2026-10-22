package record

import "github.com/domainry/domainry-foundation/entityid"

func recordAuditTestID(seed string) string {
	value, err := entityid.Derive("audevt", "runtime.record.test", seed)
	if err != nil {
		panic(err)
	}
	return value
}

func recordNotificationTestID(seed string) string {
	value, err := entityid.Derive("notevt", "runtime.record.test", seed)
	if err != nil {
		panic(err)
	}
	return value
}
