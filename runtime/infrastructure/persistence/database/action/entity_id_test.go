package action

import "github.com/domainry/domainry-foundation/entityid"

func actionAuditTestID(seed string) string {
	value, err := entityid.Derive("audevt", "runtime.action.test", seed)
	if err != nil {
		panic(err)
	}
	return value
}

func actionNotificationTestID(seed string) string {
	value, err := entityid.Derive("notevt", "runtime.action.test", seed)
	if err != nil {
		panic(err)
	}
	return value
}
