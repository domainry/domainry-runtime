package deployment

import (
	"context"
	"errors"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type readinessRepository struct {
	pingErr    error
	migration  deploymentmodel.MigrationStatus
	migrateErr error
}

func (r readinessRepository) Ping(context.Context) error { return r.pingErr }
func (r readinessRepository) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return r.migration, r.migrateErr
}

func TestRuntimeReadinessSeparatesDatabaseAndMigrationFailures(t *testing.T) {
	service := NewDeploymentRuntimeStatusApplicationService(nil, nil, readinessRepository{pingErr: errors.New("database down"), migration: deploymentmodel.MigrationStatus{Current: true}}, nil, nil, nil, nil)
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
