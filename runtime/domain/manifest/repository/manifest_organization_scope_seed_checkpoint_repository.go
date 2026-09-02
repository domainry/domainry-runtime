package repository

import "context"

// OrganizationScopeSeedCheckpointRepository records only the IDs previously
// owned by manifest bootstrap, so Runtime can retire removed claims without
// touching organization facts managed by Identity.
type OrganizationScopeSeedCheckpointRepository interface {
	ManifestOrganizationScopeSeedState(context.Context) (string, error)
	SetManifestOrganizationScopeSeedState(context.Context, string) error
}
