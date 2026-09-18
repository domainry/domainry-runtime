package model

// Projection is the private persistence result behind one public resource.
// WorkspaceID and RecordID are retained only so Runtime can safely resolve
// relation-bound files; HTTP responses must never serialize either value.
type Projection struct {
	WorkspaceID string
	RecordID    string
	Data        map[string]any
	FileRecords map[string]string
}

// Resource is the anonymous JSON response contract. Data contains only the
// manifest allowlist and Files maps declared file fields to public paths.
type Resource struct {
	ResourceKey string            `json:"resource_key"`
	Data        map[string]any    `json:"data"`
	Files       map[string]string `json:"files,omitempty"`
}

// File is a verified public file response. Content remains private to Runtime
// until every manifest, record, revocation and scan check succeeds.
type File struct {
	Filename    string
	ContentType string
	Content     []byte
}
