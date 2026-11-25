package deploymentmodel

import (
	"errors"
	"time"
)

var ( // Deployment admission failures remain stable across persistence adapters.
	ErrRuntimeReleaseAdmission    = errors.New("runtime release admission failed")
	ErrRuntimeReleaseLeaseLost    = errors.New("runtime release cohort lease lost")
	ErrRuntimeReleaseLeaseExpired = errors.New("runtime release cohort lease expired")
)

type RuntimeReleaseCohortClaim struct {
	InstanceID    string
	Identity      RuntimeReleaseIdentity
	Now           time.Time
	LeaseDuration time.Duration
}

type RuntimeReleaseCohortLease struct {
	InstanceID        string
	CombinationSHA256 string
	Generation        int64
	ExpiresAt         time.Time
}
