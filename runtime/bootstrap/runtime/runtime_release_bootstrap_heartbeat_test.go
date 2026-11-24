package runtime

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeReleaseBootstrapHeartbeatKeepsLeaseAliveUntilStartupCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", Generation: 1, ExpiresAt: time.Now().Add(45 * time.Second)}
		calls := 0
		keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), 15*time.Second, initial, time.Now, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			calls++
			lease.ExpiresAt = now.Add(45 * time.Second)
			return lease, nil
		})
		time.Sleep(32 * time.Second)
		lease, err := keeper.Stop()
		if err != nil || calls != 2 || !lease.ExpiresAt.After(initial.ExpiresAt) {
			t.Fatalf("startup heartbeat calls=%d lease=%+v error=%v", calls, lease, err)
		}
		time.Sleep(45 * time.Second)
		second, secondErr := keeper.Stop()
		if secondErr != nil || second != lease {
			t.Fatalf("second stop lease=%+v err=%v, want lease=%+v err=nil", second, secondErr, lease)
		}
	})
}

func TestRuntimeReleaseBootstrapHeartbeatReturnsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantErr := errors.Join(deploymentmodel.ErrRuntimeReleaseLeaseLost, errors.New("generation changed"))
		initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", Generation: 1, ExpiresAt: time.Now().Add(45 * time.Second)}
		keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), 15*time.Second, initial, time.Now, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, _ time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			return lease, wantErr
		})
		time.Sleep(16 * time.Second)
		lease, err := keeper.Stop()
		if lease != initial || !errors.Is(err, wantErr) {
			t.Fatalf("lease=%+v err=%v, want lease=%+v wrapped error=%v", lease, err, initial, wantErr)
		}
	})
}

func TestRuntimeReleaseBootstrapHeartbeatRetriesATemporaryFailureBeforeHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", ExpiresAt: time.Now().Add(45 * time.Second)}
		attempts := 0
		keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), 15*time.Second, initial, time.Now, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			attempts++
			if attempts == 1 {
				return lease, errors.New("database temporarily unavailable")
			}
			lease.ExpiresAt = now.Add(45 * time.Second)
			return lease, nil
		})
		time.Sleep(20 * time.Second)
		lease, err := keeper.Stop()
		if err != nil || attempts != 2 || !lease.ExpiresAt.After(initial.ExpiresAt) {
			t.Fatalf("startup did not recover before handoff: lease=%+v attempts=%d err=%v", lease, attempts, err)
		}
	})
}

func TestRuntimeReleaseBootstrapHeartbeatRejectsExpiredLeaseAtHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", ExpiresAt: time.Now().Add(5 * time.Second)}
		attempts := 0
		keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), 15*time.Second, initial, time.Now, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, _ time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			attempts++
			return lease, nil
		})
		time.Sleep(5 * time.Second)
		lease, err := keeper.Stop()
		if lease != initial || attempts != 0 || !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) {
			t.Fatalf("expired lease accepted at handoff: lease=%+v attempts=%d err=%v", lease, attempts, err)
		}
	})
}

func TestRuntimeReleaseBootstrapHeartbeatRejectsIncompleteInput(t *testing.T) {
	if heartbeat := startRuntimeReleaseBootstrapHeartbeat(nil, time.Second, deploymentmodel.RuntimeReleaseCohortLease{}, nil, nil); heartbeat != nil {
		t.Fatalf("heartbeat=%+v, want nil", heartbeat)
	}
}
