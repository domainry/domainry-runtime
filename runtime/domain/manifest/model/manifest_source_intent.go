package manifestmodel

type ManifestSourceIntentCoverage struct {
	Version       string                      `json:"version"`
	Entries       []ManifestSourceIntentEntry `json:"entries"`
	UnmappedPaths []string                    `json:"unmapped_paths"`
}
type ManifestSourceIntentEntry struct {
	SourcePath string `json:"source_path"`
	Status     string `json:"status"`
	Target     string `json:"target"`
	Note       string `json:"note,omitempty"`
}
