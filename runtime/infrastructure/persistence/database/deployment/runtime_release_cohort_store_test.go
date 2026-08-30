package deployment

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openRuntimeReleaseStore(t *testing.T, path string) (*database.RuntimeStore, RuntimeReleaseCohortStore) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, MigrationBackupDir: filepath.Join(filepath.Dir(path), "backups")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewRuntimeReleaseCohortStore(store)
}

func persistenceRuntimeReleaseIdentity(t *testing.T, marker byte) deploymentmodel.RuntimeReleaseIdentity {
	t.Helper()
	hash := func(value byte) string { return strings.Repeat(string(value), 64) }
	identity := deploymentmodel.RuntimeReleaseIdentity{
		ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion, BuildMode: "development", RuntimeVersion: "runtime-" + string(marker),
		RuntimeextContractVersion: "runtimeext-v1", RuntimeextContractSHA256: hash('a'), ConnectorContractVersion: "connector-v1", ConnectorContractSHA256: hash('b'),
		DomainSDKContractVersion: "sdk-v1", DomainSDKContractSHA256: hash('c'), DomainSDKGeneratorVersion: "generator-v1", DomainSDKBuildConstraint: "constraint-" + string(marker),
		ApplicationSchemaSnapshotSHA256: hash(marker), GeneratedSDKSHA256: hash('d'), HandlerRegistrySHA256: hash('e'), ConnectorRegistrySHA256: hash('f'),
	}
	combination, err := deploymentapplication.RuntimeReleaseCombinationSHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.CombinationSHA256 = combination
	return identity
}

func claimRuntimeRelease(t *testing.T, store RuntimeReleaseCohortStore, instance string, identity deploymentmodel.RuntimeReleaseIdentity, now time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	t.Helper()
	return store.ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{InstanceID: instance, Identity: identity, Now: now, LeaseDuration: time.Minute})
}

func TestRuntimeReleaseCohortRejectsConflictAndRotatesAfterDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	_, store := openRuntimeReleaseStore(t, path)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	firstIdentity, nextIdentity := persistenceRuntimeReleaseIdentity(t, '1'), persistenceRuntimeReleaseIdentity(t, '2')
	first, err := claimRuntimeRelease(t, store, "runtime-1", firstIdentity, now)
	if err != nil || first.Generation != 1 {
		t.Fatalf("first lease=%+v error=%v", first, err)
	}
	second, err := claimRuntimeRelease(t, store, "runtime-2", firstIdentity, now)
	if err != nil || second.Generation != first.Generation {
		t.Fatalf("same identity lease=%+v error=%v", second, err)
	}
	if _, err := claimRuntimeRelease(t, store, "runtime-conflict", nextIdentity, now); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseConflict) {
		t.Fatalf("conflicting identity error=%v", err)
	}
	if err := store.ReleaseRuntimeRelease(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseRuntimeRelease(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	rotated, err := claimRuntimeRelease(t, store, "runtime-next", nextIdentity, now.Add(time.Second))
	if err != nil || rotated.Generation != first.Generation+1 {
		t.Fatalf("rotated lease=%+v error=%v", rotated, err)
	}
}

func TestRuntimeReleaseCohortExpiryAndHeartbeatAreFenced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	_, store := openRuntimeReleaseStore(t, path)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	identity, nextIdentity := persistenceRuntimeReleaseIdentity(t, '3'), persistenceRuntimeReleaseIdentity(t, '4')
	lease, err := store.ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{InstanceID: "runtime-expiring", Identity: identity, Now: now, LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.HeartbeatRuntimeRelease(t.Context(), lease, now.Add(2*time.Second), time.Minute); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) {
		t.Fatalf("expired heartbeat error=%v", err)
	}
	rotated, err := claimRuntimeRelease(t, store, "runtime-rotated", nextIdentity, now.Add(2*time.Second))
	if err != nil || rotated.Generation != lease.Generation+1 {
		t.Fatalf("expiry rotation lease=%+v error=%v", rotated, err)
	}
	if _, err := store.HeartbeatRuntimeRelease(t.Context(), lease, now.Add(3*time.Second), time.Minute); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) {
		t.Fatalf("stale generation heartbeat error=%v", err)
	}
}

func TestRuntimeReleaseCohortConcurrentFirstClaimHasSingleIdentityWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	_, firstStore := openRuntimeReleaseStore(t, path)
	_, secondStore := openRuntimeReleaseStore(t, path)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	identities := []deploymentmodel.RuntimeReleaseIdentity{persistenceRuntimeReleaseIdentity(t, '5'), persistenceRuntimeReleaseIdentity(t, '6')}
	stores := []RuntimeReleaseCohortStore{firstStore, secondStore}
	start := make(chan struct{})
	errorsByInstance := make([]error, 2)
	leasess := make([]deploymentmodel.RuntimeReleaseCohortLease, 2)
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			leasess[index], errorsByInstance[index] = stores[index].ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{InstanceID: "runtime-" + string(rune('a'+index)), Identity: identities[index], Now: now, LeaseDuration: time.Minute})
		}(index)
	}
	close(start)
	wait.Wait()
	successes, conflicts := 0, 0
	for index, err := range errorsByInstance {
		if err == nil {
			successes++
			_ = stores[index].ReleaseRuntimeRelease(t.Context(), leasess[index])
		} else if errors.Is(err, deploymentmodel.ErrRuntimeReleaseConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent claim error[%d]=%v", index, err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d errors=%v", successes, conflicts, errorsByInstance)
	}
}

func TestRuntimeReleaseCohortConcurrentMatchingInstancesJoinSameGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	_, firstStore := openRuntimeReleaseStore(t, path)
	_, secondStore := openRuntimeReleaseStore(t, path)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	identity := persistenceRuntimeReleaseIdentity(t, '7')
	stores := []RuntimeReleaseCohortStore{firstStore, secondStore}
	start := make(chan struct{})
	leasess := make([]deploymentmodel.RuntimeReleaseCohortLease, 2)
	errorsByInstance := make([]error, 2)
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			leasess[index], errorsByInstance[index] = stores[index].ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{InstanceID: "matching-" + string(rune('a'+index)), Identity: identity, Now: now, LeaseDuration: time.Minute})
		}(index)
	}
	close(start)
	wait.Wait()
	if errorsByInstance[0] != nil || errorsByInstance[1] != nil || leasess[0].Generation == 0 || leasess[0].Generation != leasess[1].Generation {
		t.Fatalf("leases=%+v errors=%v", leasess, errorsByInstance)
	}
}

func TestRuntimeReleaseCohortSQLContractAcrossSupportedDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.db")
			runtimeStore, _ := openRuntimeReleaseStore(t, path)
			if err := runtimeStore.SetEngineForTesting(dialect); err != nil {
				t.Fatal(err)
			}
			store := NewRuntimeReleaseCohortStore(runtimeStore)
			now := time.Date(2026, 7, 23, 10, 0, 0, 123, time.UTC)
			identity := persistenceRuntimeReleaseIdentity(t, '8')
			lease, err := claimRuntimeRelease(t, store, "runtime-"+dialect, identity, now)
			if err != nil {
				t.Fatal(err)
			}
			lease, err = store.HeartbeatRuntimeRelease(t.Context(), lease, now.Add(time.Second), time.Minute)
			if err != nil || !lease.ExpiresAt.Equal(now.Add(time.Second+time.Minute)) {
				t.Fatalf("heartbeat lease=%+v error=%v", lease, err)
			}
			if err := store.ReleaseRuntimeRelease(t.Context(), lease); err != nil {
				t.Fatal(err)
			}
		})
	}
}
