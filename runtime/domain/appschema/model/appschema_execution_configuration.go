package appschemamodel

// ApplicationExecutionConfiguration is read from one catalog row so the
// accounting zone and its publication revision cannot come from two versions.
type ApplicationExecutionConfiguration struct {
	SchemaRevision string
	TimeZone       string
}
