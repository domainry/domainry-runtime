package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	monitoringremote "github.com/domainry/domainry-monitoring-sdk/remote"
	monitoringmodule "github.com/domainry/domainry-monitoring/module"
	monitoringserver "github.com/domainry/domainry-monitoring/saas"
	notificationmodule "github.com/domainry/domainry-notification/module"
	partymodule "github.com/domainry/domainry-party/module"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulermodule "github.com/domainry/domainry-scheduler/module"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
)

func runtimeTestNotificationFactory() *notificationmodule.Factory {
	factory := notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment())
	return factory
}

func runtimeTestIntegrationFactory() integrationsdk.Factory {
	return integrationmodule.NewFactory()
}

func runtimeTestPartyFactory() *partymodule.Factory {
	return partymodule.NewFactory(partymodule.Options{})
}

func runtimeTestDataExchangeFactory() dataexchangesdk.Factory {
	return dataexchangefixture.NewFactory()
}

type startupIntegrationCatalogStub struct {
	definitions []integrationsdk.ConnectorDefinition
	err         error
}

func (s startupIntegrationCatalogStub) ListConnectorDefinitions(context.Context) ([]integrationsdk.ConnectorDefinition, error) {
	return append([]integrationsdk.ConnectorDefinition(nil), s.definitions...), s.err
}

type startupContractMismatchHandler struct{}

func (startupContractMismatchHandler) Descriptor() runtimeext.HandlerDescriptor {
	return runtimeext.HandlerDescriptor{
		ActionKey: "booking.reserve", InputType: "example.com/project/actions.BookingReserveInput", OutputType: "example.com/project/actions.BookingReserveOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: "handler-v1",
	}
}

func (startupContractMismatchHandler) Invoke(context.Context, runtimeext.ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func TestRuntimeActionReadinessFailsClosedOnPublishedContractMismatch(t *testing.T) {
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(startupContractMismatchHandler{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := definitionmodel.ActionSchema{
		Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation,
		InputType: "example.com/project/actions.BookingReserveInput", OutputType: "example.com/project/actions.BookingReserveOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("c", 64),
	}
	system := actionapplication.NewSystemOperationCatalog()
	application := actionapplication.NewActionApplication(actionapplication.ActionApplicationDependencies{
		Catalog:          actionapplication.NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry),
		SystemOperations: actionapplication.NewSystemOperationExecutor(system),
	})
	err := validateRuntimeActionReadiness(application)
	if err == nil || !strings.Contains(err.Error(), "output_contract_sha256 mismatch") {
		t.Fatalf("readiness error=%v", err)
	}
}

func TestRuntimeStartupRegistryAndReadinessBoundaryGuards(t *testing.T) {
	if err := validateRuntimeActionReadiness(nil); err == nil {
		t.Fatal("nil Action Application passed readiness")
	}
	evidence := deploymentapplication.RuntimeReleaseArtifactEvidence{Verified: true}
	if got := firstRuntimeReleaseArtifactEvidence([]deploymentapplication.RuntimeReleaseArtifactEvidence{evidence}); !got.Verified {
		t.Fatalf("artifact evidence=%+v", got)
	}

	frozenHandlers := runtimeext.NewBusinessHandlerRegistry()
	frozenHandlers.Freeze()
	frozenConnectors := connector.NewRegistry()
	frozenConnectors.Freeze()
	for name, run := range map[string]func(){
		"nil handlers": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), nil, frozenConnectors, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
		},
		"unfrozen handlers": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), runtimeext.NewBusinessHandlerRegistry(), frozenConnectors, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
		},
		"nil connectors": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), frozenHandlers, nil, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
		},
		"unfrozen connectors": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), frozenHandlers, connector.NewRegistry(), runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
		},
	} {
		t.Run(name, func(t *testing.T) { assertBootstrapPanic(t, run) })
	}
}

func TestNewBuildsRunnableRuntimeAndClosesStartedWorkers(t *testing.T) {
	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	if runtime.store == nil || runtime.records == nil || runtime.identityBinding == nil {
		t.Fatal("runtime composition is incomplete")
	}
	inventory, err := runtime.ModuleInventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Modules) != 9 {
		t.Fatalf("module inventory=%+v", inventory.Modules)
	}
	wantModules := map[string]bool{"audit": true, "data_exchange": true, "identity": true, "integration": true, "lifecycle": true, "metadata": true, "notification": true, "party": true, "report": true}
	for _, module := range inventory.Modules {
		if !wantModules[module.Key] {
			t.Fatalf("unexpected module in legacy constructor inventory: %+v", module)
		}
		delete(wantModules, module.Key)
	}
	if len(wantModules) != 0 {
		t.Fatalf("module inventory is missing %v", wantModules)
	}
	if schema := runtime.records.Applications().Schema.ObjectMap(t.Context()); schema["customer"].Key == "" {
		t.Fatalf("customer schema missing: %#v", schema)
	}
	response := httptest.NewRecorder()
	runtime.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}

	StartWorkers(t.Context(), runtime)
	startedWorkers := len(runtime.workerDone)
	StartWorkers(t.Context(), runtime)
	if len(runtime.workerDone) != startedWorkers {
		t.Fatalf("duplicate StartWorkers registered supervisors: first=%d second=%d", startedWorkers, len(runtime.workerDone))
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestNewWithBusinessHandlersBuildsRuntime(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	runtime := NewWithBusinessHandlers(t.Context(), bootstrapTestConfig(t), handlers, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	if runtime.businessHandlers != handlers {
		t.Fatal("Runtime did not retain supplied business handlers")
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRuntimeOpensOneNotificationModuleBinding(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	runtime := NewProjectWithFactoriesAndDatabase(
		t.Context(), bootstrapTestConfig(t), handlers, connectors,
		runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{},
		runtimeIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory(), nil,
	)
	if runtime.notificationBinding == nil || runtime.notificationBinding.Descriptor().Mode != "module" || runtime.notificationWorkers == nil || runtime.notificationHTTP == nil {
		t.Fatalf("Notification Module composition is incomplete: binding=%v workers=%v http=%v", runtime.notificationBinding, runtime.notificationWorkers, runtime.notificationHTTP)
	}
	if runtime.store.NotificationTransactions() == nil {
		t.Fatal("Notification Module transaction publisher was not bound to the shared Runtime store")
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRuntimeOpensMonitoringModuleAndSaaSBindings(t *testing.T) {
	build := func(t *testing.T, factory monitoringsdk.Factory) *Runtime {
		t.Helper()
		handlers := runtimeext.NewBusinessHandlerRegistry()
		handlers.Freeze()
		connectors := connector.NewRegistry()
		connectors.Freeze()
		cfg := bootstrapTestConfig(t)
		cfg.RuntimeInstanceID = "monitoring-runtime"
		return NewProjectWithAllFactoriesAndDatabase(t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory(), nil, factory)
	}

	moduleRuntime := build(t, monitoringmodule.NewFactory(monitoringmodule.Options{}))
	if moduleRuntime.monitoringBinding == nil || moduleRuntime.monitoringBinding.Descriptor().Mode != monitoringsdk.DeploymentModeModule {
		t.Fatalf("module binding=%#v", moduleRuntime.monitoringBinding)
	}
	if metrics := moduleRuntime.monitoringBinding.Metrics(t.Context()); metrics["runtime_id"] != "monitoring-runtime" {
		t.Fatalf("module metrics=%#v", metrics)
	}
	if err := moduleRuntime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	service := httptest.NewServer(monitoringserver.New(monitoringserver.Options{BearerToken: "secret"}).Routes())
	defer service.Close()
	saasRuntime := build(t, monitoringremote.NewFactory(monitoringremote.Config{Endpoint: service.URL, Token: "secret", Client: service.Client()}))
	if saasRuntime.monitoringBinding == nil || saasRuntime.monitoringBinding.Descriptor().Mode != monitoringsdk.DeploymentModeSaaS {
		t.Fatalf("saas binding=%#v", saasRuntime.monitoringBinding)
	}
	if metrics := saasRuntime.monitoringBinding.Metrics(t.Context()); metrics["runtime_id"] != "monitoring-runtime" {
		t.Fatalf("saas metrics=%#v", metrics)
	}
	if err := saasRuntime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRuntimeOpensExtractedSchedulerModuleBinding(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "scheduler-runtime"
	runtime := NewProjectWithOwnerFactoriesAndDatabase(t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), nil, schedulermodule.NewFactory(schedulermodule.Options{}), dataexchangefixture.NewFactory(), runtimeTestIntegrationFactory(), nil)
	if runtime.schedulerBinding == nil || runtime.schedulerBinding.Descriptor().Mode != schedulersdk.DeploymentModeModule {
		t.Fatalf("scheduler binding=%#v", runtime.schedulerBinding)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRuntimeJoinsSharedReleaseCohortBeforeServingTraffic(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "runtime-release-a"
	identity := bootstrapRuntimeReleaseIdentity(t, '1')
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	first := NewProjectWithIdentity(t.Context(), cfg, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	t.Cleanup(func() { _ = first.CloseContext(t.Context()) })
	firstLease := first.runtimeReleaseLease()
	if firstLease.InstanceID != cfg.RuntimeInstanceID || firstLease.Generation == 0 || first.releaseAdmission.Check() != nil {
		t.Fatalf("first release lease=%+v admission=%v", firstLease, first.releaseAdmission.Check())
	}

	matchingConfig := cfg
	matchingConfig.RuntimeInstanceID = "runtime-release-b"
	matching := NewProjectWithIdentity(t.Context(), matchingConfig, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	matchingLease := matching.runtimeReleaseLease()
	if matchingLease.Generation != firstLease.Generation {
		t.Fatalf("matching generations first=%d second=%d", firstLease.Generation, matchingLease.Generation)
	}
	if err := matching.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	conflicting := identity
	conflicting.ApplicationSchemaSnapshotSHA256 = strings.Repeat("2", 64)
	combination, err := deploymentapplication.RuntimeReleaseCombinationSHA256(conflicting)
	if err != nil {
		t.Fatal(err)
	}
	conflicting.CombinationSHA256 = combination
	conflictConfig := cfg
	conflictConfig.RuntimeInstanceID = "runtime-release-conflict"
	defer func() {
		value := recover()
		err, ok := value.(error)
		if value == nil || !ok || !errors.Is(err, deploymentmodel.ErrRuntimeReleaseConflict) {
			t.Fatalf("conflicting Runtime startup panic=%v", value)
		}
	}()
	NewProjectWithIdentity(t.Context(), conflictConfig, handlers, connectors, conflicting, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
}

func bootstrapRuntimeReleaseIdentity(t *testing.T, marker byte) deploymentmodel.RuntimeReleaseIdentity {
	t.Helper()
	hash := func(value byte) string { return strings.Repeat(string(value), 64) }
	identity := deploymentmodel.RuntimeReleaseIdentity{
		ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion, BuildMode: "development", RuntimeVersion: "runtime-test",
		RuntimeextContractVersion: "runtimeext-test", RuntimeextContractSHA256: hash('a'), ConnectorContractVersion: "connector-test", ConnectorContractSHA256: hash('b'),
		DomainSDKContractVersion: "sdk-test", DomainSDKContractSHA256: hash('c'), DomainSDKGeneratorVersion: "generator-test", DomainSDKBuildConstraint: "constraint-test",
		ApplicationSchemaSnapshotSHA256: hash(marker), GeneratedSDKSHA256: hash('d'), HandlerRegistrySHA256: hash('e'), ConnectorRegistrySHA256: hash('f'),
	}
	combination, err := deploymentapplication.RuntimeReleaseCombinationSHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.CombinationSHA256 = combination
	return identity
}

func TestProjectRuntimeReleaseIntegrityTracksLiveSchemaAndFrozenRegistries(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "runtime-integrity"
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	identity := bootstrapRuntimeReleaseIdentity(t, '1')
	var err error
	identity.HandlerRegistrySHA256, err = deploymentmodel.RuntimeRegistrySHA256("domainry-handler-registry-v1", handlers.Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	identity.ConnectorRegistrySHA256, err = deploymentmodel.RuntimeRegistrySHA256("domainry-connector-registry-v1", connectors.Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	identity.CombinationSHA256, err = deploymentapplication.RuntimeReleaseCombinationSHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewProjectWithIdentity(t.Context(), cfg, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	t.Cleanup(func() { _ = runtime.CloseContext(t.Context()) })
	if err := runtime.releaseIntegrity.SchemaReadiness(t.Context()); err != nil {
		t.Fatalf("initial Schema readiness=%v", err)
	}
	if err := runtime.releaseIntegrity.RegistryReadiness(t.Context()); err != nil {
		t.Fatalf("initial Registry readiness=%v", err)
	}
	if _, err := runtime.store.DB().ExecContext(t.Context(), "UPDATE _application_schema_projection SET schema_hash = ? WHERE id = ?", "drifted-schema", "current"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.releaseIntegrity.SchemaReadiness(t.Context()); !errors.Is(err, deploymentapplication.ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("drifted Schema readiness=%v", err)
	}
}

func TestNewBuildsObjectlessConfiguringRuntimeForDirectAuthoring(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.ManifestPath = writeManifestLoaderFixture(t, `{"schema_version":"2","template_id":"direct-authoring-project","version":"0.0.0-configuring","source_blueprint_id":"runtime-direct-authoring-v4","objects":[]}`)
	cfg.AllowEmptyAuthoringManifest = true
	cfg.RuntimeAllowDevIdentityHeaders = true
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	defer runtime.CloseContext(t.Context())
	for key, object := range runtime.records.Applications().Schema.ObjectMap(t.Context()) {
		if runtimeOwned, _ := object.Config["runtime_owned"].(bool); !runtimeOwned {
			t.Fatalf("configuring Runtime leaked bootstrap business object %s: %#v", key, object)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities/index", nil)
	request.Header.Set("X-User-ID", "admin")
	request.Header.Set("X-Role", "admin")
	response := httptest.NewRecorder()
	runtime.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capability index status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGlobalValidationAndDeliveryGateMoveOwnedRuntimeToReady(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	raw, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var canonical manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &canonical); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = writeManifestLoaderFixture(t, string(raw))
	state := provision.LifecycleState{
		Version: "runtime-lifecycle-v1", Status: provision.LifecycleStatusConfiguring,
		RuntimeID: "validation-e2e", BuilderTaskID: "builder-task-e2e", SnapshotHash: "initial",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	lifecycleRaw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provision.LifecyclePath(cfg.ManifestPath), lifecycleRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	defer runtime.CloseContext(t.Context())
	permissions := []string{
		"workspace.admin", "scheduler.definition.read", "ops.workflow.read", "workflow.process.read",
		"integration.audit.view", "integration.catalog.view",
	}
	dataPermissions := []accessfixture.DataPolicyFixture{}
	for _, object := range canonical.Objects {
		permissions = append(permissions, object.Key+".read")
		dataPermissions = append(dataPermissions, accessfixture.DataPolicyFixture{ObjectKey: object.Key, Scope: "all_records", Read: true})
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "admin", Permissions: permissions, DataPolicies: dataPermissions})
	if _, err := runtime.records.Applications().BusinessSystem.RuntimeStateSnapshot(t.Context(), admin); err != nil {
		t.Fatalf("direct Runtime-state snapshot: %v params=%#v cause=%v", err, apperror.ParamsOf(err), errors.Unwrap(err))
	}
	if _, err := runtime.records.Applications().BusinessSystem.Snapshot(t.Context(), admin); err != nil {
		t.Fatalf("direct domain-system snapshot: %v params=%#v cause=%v", err, apperror.ParamsOf(err), errors.Unwrap(err))
	}
	snapshot := runtimeAuthoringRequest(t, runtime, http.MethodGet, "/domain-system-snapshot", nil, false)
	resources := []any{}
	for _, value := range snapshot["resource_sources"].([]any) {
		resource := value.(map[string]any)
		if disabled, _ := resource["disabled"].(bool); disabled {
			continue
		}
		resources = append(resources, map[string]any{"resource_type": resource["resource_type"], "resource_key": resource["resource_key"]})
	}
	if len(resources) == 0 {
		t.Fatal("canonical Runtime snapshot has no resources for coverage validation")
	}
	invalidReport := runtimeAuthoringRequest(t, runtime, http.MethodPost, "/domain-system-validation", map[string]any{"coverage": map[string]any{
		"version": "runtime-authoring-coverage-v1", "requirements": []any{map[string]any{
			"requirement_id": "unmapped-runtime", "capability_keys": []any{"schema.object"},
			"resources": []any{map[string]any{"resource_type": "object", "resource_key": "missing"}}, "scenario_ids": []any{"unmapped-runtime.startup"},
		}},
	}}, false)
	if valid, _ := invalidReport["valid"].(bool); valid {
		t.Fatalf("unknown coverage resource passed global validation: %#v", invalidReport)
	}
	rejected, found, err := provision.ReadLifecycle(cfg.ManifestPath)
	if err != nil || !found || rejected.Status != provision.LifecycleStatusConfiguring {
		t.Fatalf("invalid coverage advanced lifecycle: lifecycle=%#v found=%v err=%v report=%#v", rejected, found, err, invalidReport)
	}
	coverage := map[string]any{
		"version": "runtime-authoring-coverage-v1", "requirements": []any{map[string]any{
			"requirement_id": "canonical-runtime", "capability_keys": []any{"schema.object"}, "resources": resources, "scenario_ids": []any{"canonical-runtime.startup"},
		}},
	}
	report := runtimeAuthoringRequest(t, runtime, http.MethodPost, "/domain-system-validation", map[string]any{"coverage": coverage}, false)
	if valid, _ := report["valid"].(bool); !valid {
		t.Fatalf("canonical Runtime did not pass global validation: %#v", report)
	}
	if report["version"] != "runtime-authoring-validation-v2" {
		t.Fatalf("global validation did not use the complete configuration contract: %#v", report)
	}
	checks, ok := report["checks"].(map[string]any)
	if !ok {
		t.Fatalf("global validation omitted coverage checks: %#v", report)
	}
	for _, category := range []string{
		"schema", "resource_sources",
		"runtime_state.automation", "runtime_state.integrations", "runtime_state.reports", "runtime_state.scheduler",
	} {
		if checks["configuration."+category] != "ok" {
			t.Fatalf("global validation did not cover %s: %#v", category, report)
		}
	}
	for _, check := range []string{"cross_resource_references", "cycles", "permission_closure", "foundation_usage", "connector_readiness"} {
		if checks[check] != "ok" {
			t.Fatalf("global validation did not pass %s: %#v", check, report)
		}
	}
	updated, found, err := provision.ReadLifecycle(cfg.ManifestPath)
	if err != nil || !found || updated.Status != provision.LifecycleStatusVerifying || updated.SnapshotHash != fmt.Sprint(report["snapshot_hash"]) {
		t.Fatalf("lifecycle=%#v found=%v err=%v report=%#v", updated, found, err, report)
	}
	step := func(label, path string, status int, responseHash string) map[string]any {
		return map[string]any{"label": label, "method": "GET", "path": path, "expected_status": []any{status}, "actual_status": status, "request_hash": strings.Repeat("a", 64), "response_hash": responseHash, "passed": true}
	}
	replayOne, replayTwo := step("replay-one", "/objects/customer/records", 200, strings.Repeat("b", 64)), step("replay-two", "/objects/customer/records", 200, strings.Repeat("b", 64))
	replayOne["idempotency_key"], replayTwo["idempotency_key"] = "customer-create-1", "customer-create-1"
	replayTwo["idempotency_replayed"] = true
	scenarios := []any{map[string]any{
		"version": "runtime-authoring-scenario-evidence-v1", "scenario_id": "canonical-runtime.startup", "passed": true,
		"categories":        []any{"success", "permission_denied", "precondition_rejected", "atomic_rollback", "idempotent_replay", "audit", "event", "outbox"},
		"before_state_hash": "same", "after_state_hash": "same", "steps": []any{
			step("success", "/objects/customer/records/customer-1", 200, strings.Repeat("c", 64)),
			step("denied", "/objects/customer/records/customer-1", 403, strings.Repeat("d", 64)),
			step("precondition", "/objects/customer/records/customer-1/actions/update", 422, strings.Repeat("e", 64)),
			replayOne, replayTwo, step("audit", "/audit-events", 200, strings.Repeat("f", 64)),
			step("event", "/events", 200, strings.Repeat("1", 64)), step("outbox", "/integration-outbox", 200, strings.Repeat("2", 64)),
		},
	}}
	alteredCoverage := map[string]any{
		"version": "runtime-authoring-coverage-v1", "requirements": []any{map[string]any{
			"requirement_id": "altered-after-validation", "capability_keys": []any{"schema.object"}, "resources": resources, "scenario_ids": []any{"canonical-runtime.startup"},
		}},
	}
	alteredDelivery := runtimeAuthoringRequest(t, runtime, http.MethodPost, "/domain-system-delivery-verification", map[string]any{
		"version": "runtime-authoring-delivery-evidence-v1", "binding": report["binding"], "coverage": alteredCoverage, "scenarios": scenarios,
	}, false)
	if valid, _ := alteredDelivery["valid"].(bool); valid {
		t.Fatalf("delivery accepted a coverage ledger changed after global validation: %#v", alteredDelivery)
	}
	stillVerifying, found, err := provision.ReadLifecycle(cfg.ManifestPath)
	if err != nil || !found || stillVerifying.Status != provision.LifecycleStatusVerifying {
		t.Fatalf("invalid delivery changed lifecycle: lifecycle=%#v found=%v err=%v", stillVerifying, found, err)
	}
	delivery := runtimeAuthoringRequest(t, runtime, http.MethodPost, "/domain-system-delivery-verification", map[string]any{
		"version": "runtime-authoring-delivery-evidence-v1", "binding": report["binding"], "coverage": coverage, "scenarios": scenarios,
	}, false)
	if valid, _ := delivery["valid"].(bool); !valid || fmt.Sprint(delivery["evidence_hash"]) == "" {
		t.Fatalf("bound delivery evidence was rejected: %#v", delivery)
	}
	ready, found, err := provision.ReadLifecycle(cfg.ManifestPath)
	if err != nil || !found || ready.Status != provision.LifecycleStatusReady {
		t.Fatalf("verified delivery did not enter ready: lifecycle=%#v found=%v err=%v", ready, found, err)
	}
}

func runtimeAuthoringRequest(t *testing.T, runtime *Runtime, method string, path string, body map[string]any, metadataMutation bool) map[string]any {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, payload)
	ctx := operationscontract.WithBuilderTaskID(request.Context(), "builder-task-e2e")
	ctx = requestcontext.WithWorkspaceID(ctx, "workspace-primary")
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Builder-Task-ID", "builder-task-e2e")
	if metadataMutation {
		request.Header.Set("Idempotency-Key", "e2e-"+path)
		request.Header.Set("Expected-Schema-Hash", "empty")
	}
	response := httptest.NewRecorder()
	runtime.Routes().ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
	}
	result := map[string]any{}
	if response.Body.Len() > 0 {
		var decoded any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s decode response: %v body=%s", method, path, err, response.Body.String())
		}
		if object, ok := decoded.(map[string]any); ok {
			result = object
		} else {
			result["value"] = decoded
		}
	}
	return result
}

func TestNewRestoresPublishedNotificationTemplates(t *testing.T) {
	cfg := bootstrapTestConfigForManifest(t, "hr-personnel.json")
	raw, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest, "actions")
	delete(manifest, "workflows")
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(t.TempDir(), "notification-templates.json")
	if err := os.WriteFile(cfg.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	if len(runtime.manifest.NotificationTemplates) == 0 {
		t.Fatal("published notification templates were not restored")
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBindHTTPRejectsNilRuntime(t *testing.T) {
	assertBootstrapPanic(t, func() { BindHTTP(t.Context(), nil) })
}

func TestNewRejectsNilContextInvalidSecurityAndMissingManifest(t *testing.T) {
	assertBootstrapPanic(t, func() {
		New(nil, config.Config{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	})
	assertBootstrapPanic(t, func() {
		New(t.Context(), config.Config{
			Environment:                    "production",
			RuntimeAllowDevIdentityHeaders: true,
		}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	})
	assertBootstrapPanic(t, func() {
		New(t.Context(), config.Config{ManifestPath: filepath.Join(t.TempDir(), "missing.json")}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	})
	assertBootstrapPanic(t, func() {
		cfg := bootstrapTestConfig(t)
		cfg.DatabaseDriver = "unsupported"
		New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	})
}

func TestNewPropagatesNotificationAndServiceAssemblyFailures(t *testing.T) {
	invalidNotification := bootstrapTestConfig(t)
	invalidNotification.ManifestPath = writeManifestLoaderFixture(t, `{
		"schema_version":"2",
		"objects":[{"key":"account","fields":[{"key":"name","type":"text"}]}],
		"notification_templates":[{"key":"invalid","channel":"email","status":"published","version":1,"default_locale":"en-US","locales":{"en-US":{"subject":"Test","text":"Test"}}}]
	}`)
	invalidNotification.SkipManifestValidation = true
	assertBootstrapPanic(t, func() {
		New(t.Context(), invalidNotification, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory())
	})

}

func TestPrepareRuntimeManifestValidatesAndCanSkipDomainValidation(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	manifest, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Objects) == 0 {
		t.Fatal("prepared manifest has no objects")
	}
	cfg.SkipManifestValidation = true
	if _, err := prepareRuntimeManifest(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	catalog := startupIntegrationCatalogStub{definitions: []integrationsdk.ConnectorDefinition{{Key: "owner_connector", Definition: json.RawMessage(`{"key":"owner_connector"}`)}}}
	manifest.Integrations.Connectors = []connectormodel.ConnectorSchema{{Key: "manifest_must_not_own_catalog"}}
	withCatalog, err := addIntegrationOwnerValidationCatalog(t.Context(), manifest, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(withCatalog.Integrations.Connectors) != 1 || withCatalog.Integrations.Connectors[0].Key != "owner_connector" {
		t.Fatalf("owner catalog did not replace manifest definitions: %#v", withCatalog.Integrations.Connectors)
	}
	if len(manifest.Integrations.Connectors) != 1 || manifest.Integrations.Connectors[0].Key != "manifest_must_not_own_catalog" {
		t.Fatalf("transient owner catalog mutated the seed manifest: %#v", manifest.Integrations.Connectors)
	}
	if _, err := addIntegrationOwnerValidationCatalog(t.Context(), withCatalog, catalog); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRuntimeManifestRejectsInvalidDomainManifest(t *testing.T) {
	path := writeManifestLoaderFixture(t, `{"schema_version":"2","objects":[{"key":""}]}`)
	if _, err := prepareRuntimeManifest(t.Context(), config.Config{ManifestPath: path}); err == nil {
		t.Fatal("invalid domain manifest must fail validation")
	}
}

func TestManifestValidationCatalogPropagatesCatalogFailure(t *testing.T) {
	if _, err := replaceIntegrationConnectorValidationProjection(manifestmodel.ManifestSchema{}, nil, errors.New("catalog unavailable")); err == nil {
		t.Fatal("catalog failure must propagate")
	}
	if _, err := addIntegrationOwnerValidationCatalog(t.Context(), manifestmodel.ManifestSchema{}, startupIntegrationCatalogStub{err: errors.New("catalog unavailable")}); err == nil {
		t.Fatal("Integration owner catalog failure must propagate")
	}
}

func TestPrepareRuntimeManifestPropagatesValidationCatalogFailure(t *testing.T) {
	failure := errors.New("catalog unavailable")
	if _, err := prepareRuntimeManifestWithCatalog(t.Context(), bootstrapTestConfig(t), func(manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error) {
		return manifestmodel.ManifestSchema{}, failure
	}); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
}

func TestRestoreRuntimeMetadataHonorsCancelledContext(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	manifest, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, err := prepareRuntimeStore(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := restoreRuntimeMetadata(ctx, store, manifest); err == nil {
		t.Fatal("cancelled metadata restoration must fail")
	}
}

func bootstrapTestConfig(t *testing.T) config.Config {
	t.Helper()
	return bootstrapTestConfigForManifest(t, "domain-only-minimal.json")
}

func bootstrapTestConfigForManifest(t *testing.T, manifestName string) config.Config {
	t.Helper()
	return config.Config{
		AppLocale:                  "en-US",
		IdentityWorkspaceID:        "workspace-primary",
		IdentityAudience:           "domainry-runtime",
		NotificationTenantID:       "tenant-primary",
		NotificationWorkspaceID:    "workspace-primary",
		NotificationApplicationKey: "domainry-runtime",
		PartyTenantID:              "tenant-primary",
		PartyWorkspaceID:           "workspace-primary",
		PartyApplicationKey:        "domainry-runtime",
		DatabaseDriver:             "sqlite",
		DBPath:                     filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:               filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", manifestName),
		UploadDir:                  filepath.Join(t.TempDir(), "uploads"),
		SchedulerPollInterval:      5 * time.Millisecond,
		SchedulerBatchSize:         5,
		HTTPShutdownTimeout:        time.Second,
		SchedulerLeaseTTL:          time.Minute,
		SchedulerMaxCatchupWindows: 1,
	}
}

func assertBootstrapPanic(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected bootstrap panic")
		}
	}()
	run()
}
