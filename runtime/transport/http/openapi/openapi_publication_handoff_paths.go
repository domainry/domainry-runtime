package openapi

// addPublicationHandoffOpenAPIPaths publishes only the Runtime-owned durable
// publication handoff read. Integration-owned product routes and schemas are
// contributed by the mounted Integration module Surface.
func addPublicationHandoffOpenAPIPaths(paths map[string]any) {
	paths["/business/publication-handoffs/{messageID}"] = map[string]any{
		"get": openAPIOperation("getBusinessPublicationHandoff", "Publication handoff", "Read the current user's redacted Runtime publication handoff result", openAPIAdminSecurity(), openAPIPathParameter("messageID", "Runtime publication message ID"), openAPIJSONResponse("Publication handoff", openAPIObject(nil))),
	}
}
