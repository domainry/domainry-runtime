package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeReleaseBootstrapHeartbeatKeepsLeaseAliveUntilStartupCompletes(t *testing.T) {
	initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", Generation: 1, ExpiresAt: time.Now().UTC()}
	var calls atomic.Int64
	keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), time.Millisecond, initial, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease) (deploymentmodel.RuntimeReleaseCohortLease, error) {
		calls.Add(1)
		lease.ExpiresAt = lease.ExpiresAt.Add(time.Second)
		return lease, nil
	})
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	lease, err := keeper.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 || !lease.ExpiresAt.After(initial.ExpiresAt) {
		t.Fatalf("startup heartbeat calls=%d lease=%+v initial=%+v", calls.Load(), lease, initial)
	}
	second, secondErr := keeper.Stop()
	if secondErr != nil || second != lease {
		t.Fatalf("second stop lease=%+v err=%v, want lease=%+v", second, secondErr, lease)
	}
}

func TestRuntimeReleaseBootstrapHeartbeatReturnsFailure(t *testing.T) {
	wantErr := errors.New("lease expired")
	initial := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-a", Generation: 1}
	called := make(chan struct{}, 1)
	keeper := startRuntimeReleaseBootstrapHeartbeat(t.Context(), time.Millisecond, initial, func(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease) (deploymentmodel.RuntimeReleaseCohortLease, error) {
		called <- struct{}{}
		return lease, wantErr
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("startup heartbeat was not called")
	}
	lease, err := keeper.Stop()
	if lease != initial || !errors.Is(err, wantErr) {
		t.Fatalf("lease=%+v err=%v, want lease=%+v wrapped error=%v", lease, err, initial, wantErr)
	}
}

func TestRuntimeReleaseBootstrapHeartbeatRejectsIncompleteInput(t *testing.T) {
	if heartbeat := startRuntimeReleaseBootstrapHeartbeat(nil, time.Second, deploymentmodel.RuntimeReleaseCohortLease{}, nil); heartbeat != nil {
		t.Fatalf("heartbeat=%+v, want nil", heartbeat)
	}
}
