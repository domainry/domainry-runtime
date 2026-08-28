package integrationmodel

// Runtime integration enums are owner facts shared by validators and authoring projection.
func RuntimeConnectorTypes() []string { return []string{"http", "mock", "webhook"} }
func RuntimeConnectorMethods() []string {
	return []string{"DELETE", "GET", "PATCH", "POST", "PUT", "SMTP"}
}
func RuntimeConnectorExecutionModes() []string { return []string{"async", "operation", "sync"} }
func RuntimeConnectorSideEffects() []string    { return []string{"read", "reserve", "write"} }
func RuntimeConnectorProtocolFieldTypes() []string {
	return []string{"text", "long_text", "integer", "decimal", "boolean", "date", "datetime", "json", "file"}
}
func RuntimeIntegrationConnectionStatuses() []string {
	return []string{"draft", "configured", "verified", "active", "degraded", "disabled"}
}
func RuntimeIntegrationOutboxStatuses() []string {
	return []string{"cancelled", "dead_letter", "delivered", "failed", "quarantined", "queued", "read", "sending", "sent"}
}
func RuntimeIntegrationInvocationStatuses() []string {
	return []string{"queued", "running", "succeeded", "failed", "cancelled"}
}
func RuntimeIntegrationEventStatuses() []string {
	return []string{"received", "processing", "processed", "ignored", "failed", "dead_letter", "quarantined"}
}
