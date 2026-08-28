package deploymentmodel

// DeploymentFrontendCapabilityState is the neutral persistence envelope.
type DeploymentFrontendCapabilityState struct {
	WorkspaceID  string
	Revision     int64
	ManifestJSON []byte
	UpdatedAt    string
}
