package runtime

import (
	"context"
	"errors"

	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	"github.com/domainry/domainry-monitoring-sdk/modulehost"
	"github.com/domainry/domainry-monitoring-sdk/saashost"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
)

type monitoringModuleHost struct {
	status   *deploymentapplication.DeploymentRuntimeStatusApplicationService
	identity modulehost.Identity
}

func newMonitoringModuleHost(status *deploymentapplication.DeploymentRuntimeStatusApplicationService, projectKey, schemaVersion string) monitoringModuleHost {
	return monitoringModuleHost{status: status, identity: modulehost.Identity{TemplateID: projectKey, TemplateVersion: schemaVersion}}
}

func (h monitoringModuleHost) Identity() modulehost.Identity {
	return h.identity
}
func (h monitoringModuleHost) Storage() modulehost.Storage     { return monitoringStorage{h.status} }
func (h monitoringModuleHost) Migration() modulehost.Migration { return monitoringMigration{h.status} }
func (h monitoringModuleHost) Scheduler() modulehost.Component { return monitoringScheduler{h.status} }
func (h monitoringModuleHost) Lifecycle() modulehost.Component { return monitoringLifecycle{h.status} }
func (h monitoringModuleHost) Metrics() modulehost.Metrics     { return monitoringMetrics{h.status} }

type monitoringStorage struct {
	status *deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (s monitoringStorage) Status(ctx context.Context) (map[string]any, error) {
	return s.status.MonitoringStorageStatus(ctx)
}
func (s monitoringStorage) Readiness(ctx context.Context) error {
	return s.status.StorageReadiness(ctx)
}

type monitoringMigration struct {
	status *deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (m monitoringMigration) Status(ctx context.Context) (modulehost.Status, error) {
	value, err := m.status.MonitoringMigrationStatus(ctx)
	return modulehost.Status{Current: value.Current, Payload: value}, err
}
func (m monitoringMigration) Readiness(ctx context.Context) error {
	return m.status.MigrationReadiness(ctx)
}
func (m monitoringMigration) Telemetry(ctx context.Context) (int, bool, error) {
	return m.status.MigrationTelemetry(ctx)
}

type monitoringScheduler struct {
	status *deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (s monitoringScheduler) Observe(ctx context.Context) (map[string]any, error) {
	return s.status.MonitoringSchedulerStatus(ctx)
}

type monitoringLifecycle struct {
	status *deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (s monitoringLifecycle) Observe(ctx context.Context) (map[string]any, error) {
	return s.status.MonitoringLifecycleStatus(ctx)
}

type monitoringMetrics struct {
	status *deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (m monitoringMetrics) Observe(ctx context.Context) (map[string]any, map[string]string) {
	return m.status.MonitoringMetricSections(ctx)
}

var _ modulehost.Host = monitoringModuleHost{}

func openMonitoringBinding(ctx context.Context, factory monitoringsdk.Factory, application monitoringsdk.ApplicationRef, host monitoringModuleHost) (monitoringsdk.Binding, error) {
	var (
		binding monitoringsdk.Binding
		err     error
	)
	switch typed := factory.(type) {
	case modulehost.Factory:
		binding, err = typed.OpenModule(ctx, application, host)
	case saashost.Factory:
		binding, err = typed.OpenSaaS(ctx, application, host)
	default:
		binding, err = factory.Open(ctx, application)
	}
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, errors.New("Monitoring SDK Factory returned no Binding")
	}
	if err := binding.Descriptor().Validate(); err != nil {
		return nil, err
	}
	return binding, nil
}
