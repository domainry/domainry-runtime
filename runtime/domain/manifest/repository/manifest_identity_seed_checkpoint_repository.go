package repository

import "context"

// IdentitySeedCheckpointRepository persists the last manifest Identity seed
// version materialized by the Runtime.
type IdentitySeedCheckpointRepository interface {
	ManifestIdentitySeedSyncedVersion(context.Context) (string, error)
	SetManifestIdentitySeedSyncedVersion(context.Context, string) error
}
