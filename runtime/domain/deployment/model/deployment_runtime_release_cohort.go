package deploymentmodel

import (
	"errors"
	"fmt"
	"time"
)

var ( // Deployment admission failures remain stable across persistence adapters.
	ErrRuntimeReleaseAdmission = errors.New("runtime release admission failed")
	ErrRuntimeReleaseConflict  = errors.New("runtime release cohort identity conflict")
	ErrRuntimeReleaseLeaseLost = errors.New("runtime release cohort lease lost")
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

type RuntimeReleaseCohortConflict struct {
	Active  RuntimeReleaseIdentity
	Joining RuntimeReleaseIdentity
}

func (e RuntimeReleaseCohortConflict) Error() string {
	return fmt.Sprintf("%v: active=%s joining=%s", ErrRuntimeReleaseConflict, e.Active.CombinationSHA256, e.Joining.CombinationSHA256)
}

func (e RuntimeReleaseCohortConflict) Unwrap() error { return ErrRuntimeReleaseConflict }
