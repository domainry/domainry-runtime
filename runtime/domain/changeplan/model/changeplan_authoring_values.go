package changeplanmodel

// These value sets are the single owner-owned facts shared by validation and
// capability discovery. Fresh slices prevent callers from mutating the model.
func BusinessChangeOperations() []string {
	return []string{"archive", "create", "delete", "noop", "update"}
}

func BusinessChangeKinds() []string {
	return []string{"additive", "breaking", "compatible", "destructive"}
}

func BusinessChangeRiskLevels() []string {
	return []string{"critical", "high", "low", "medium"}
}

func BusinessResourceOwners() []string {
	return []string{"builder", "manual", "platform", "plugin", "template", "unknown"}
}

func BusinessRollbackStrategies() []string {
	return []string{"append_only_version", "compensating_change_plan", "manual_compensation", "new_published_version"}
}
