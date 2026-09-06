package manifestmodel

// WorkspaceProvisionProjection is application-authored data created in the
// same transaction as its owning Workspace.
type WorkspaceProvisionProjection struct {
	Key       string         `json:"key"`
	ObjectKey string         `json:"object_key"`
	Scope     string         `json:"scope"`
	Data      map[string]any `json:"data"`
}
