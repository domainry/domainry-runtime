package repository

import (
	"context"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

// RuntimeReleaseCohortRepository serializes Deployment process admission against the
// shared Runtime database. The implementation must make Claim atomic across
// every supported SQL dialect.
type RuntimeReleaseCohortRepository interface {
	ClaimRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortClaim) (deploymentmodel.RuntimeReleaseCohortLease, error)
	HeartbeatRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortLease, time.Time, time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error)
	ReleaseRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortLease) error
}
