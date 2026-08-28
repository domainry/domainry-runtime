package deployment

import (
	"context"
	"errors"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type readinessRepository struct {
	pingErr    error
	migration  deploymentmodel.MigrationStatus
	migrateErr error
}

type warningLifecycleHealth struct{}

func (warningLifecycleHealth) HealthForSystem(context.Context, principalmodel.SystemScope, time.Time) (map[string]any, error) {
	return map[string]any{"warning": true, "eligible_backlog": int64(1200), "failure_total": int64(1)}, nil
}

type readinessScheduler struct{}

func (readinessScheduler) Status(context.Context, principalmodel.SystemScope) (map[string]any, error) {
	return map[string]any{"runtime_available": true}, nil
}

func (r readinessRepository) Ping(context.Context) error { return r.pingErr }
func (r readinessRepository) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return r.migration, r.migrateErr
}

func TestHealthSnapshotStaysLowCostAndExcludesBusinessAggregates(t *testing.T) {
	service := NewDeploymentRuntimeStatusApplicationService("runtime", "v1", nil, readinessScheduler{}, readinessRepository{migration: deploymentmodel.MigrationStatus{Current: true}}, nil, nil, nil, nil)
	payload := service.Health(t.Context())
	for _, forbidden := range []string{"domain", "audit", "workflow", "idempotency"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("health snapshot contains high-cost %s aggregate: %#v", forbidden, payload)
		}
	}
	if payload["status"] != "ok" {
		t.Fatalf("health payload=%#v", payload)
	}
}

func TestRuntimeReadinessSeparatesDatabaseAndMigrationFailures(t *testing.T) {
	service := NewDeploymentRuntimeStatusApplicationService("runtime", "v1", nil, nil, readinessRepository{pingErr: errors.New("database down"), migration: deploymentmodel.MigrationStatus{Current: true}}, nil, nil, nil, nil)
	if err := service.StorageReadiness(t.Context()); err == nil {
		t.Fatal("database failure was accepted")
	}
	service.repository = readinessRepository{migration: deploymentmodel.MigrationStatus{Current: false, Pending: 2}}
	if err := service.MigrationReadiness(t.Context()); err == nil {
		t.Fatal("pending migration was accepted")
	}
	pending, current, err := service.MigrationTelemetry(t.Context())
	if err != nil || pending != 2 || current {
		t.Fatalf("migration telemetry pending=%d current=%v err=%v", pending, current, err)
	}
}

func TestLifecycleBacklogAndFailuresEnterHealthWarnings(t *testing.T) {
	service := NewDeploymentRuntimeStatusApplicationService("runtime", "v1", nil, readinessScheduler{}, readinessRepository{migration: deploymentmodel.MigrationStatus{Current: true}}, nil, nil, nil, nil)
	service.ConfigureLifecycleHealth(t.Context(), warningLifecycleHealth{})
	payload := service.Health(t.Context())
	checks := payload["checks"].(map[string]string)
	warnings := payload["warnings"].(map[string]any)
	if payload["status"] != "degraded" || checks["lifecycle"] != "warning" || warnings["lifecycle"] == nil {
		t.Fatalf("health payload=%#v", payload)
	}
}
