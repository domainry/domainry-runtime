package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodule "github.com/domainry/domainry-notification/module"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
)

func runtimeTestNotificationFactory() *notificationmodule.Factory {
	factory := notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment())
	return factory
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
			newWithExtensions(t.Context(), bootstrapTestConfig(t), nil, frozenConnectors, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
		},
		"unfrozen handlers": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), runtimeext.NewBusinessHandlerRegistry(), frozenConnectors, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
		},
		"nil connectors": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), frozenHandlers, nil, runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
		},
		"unfrozen connectors": func() {
			newWithExtensions(t.Context(), bootstrapTestConfig(t), frozenHandlers, connector.NewRegistry(), runtimehttp.RuntimeReleaseIdentity{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
		},
	} {
		t.Run(name, func(t *testing.T) { assertBootstrapPanic(t, run) })
	}
}

func TestNewBuildsRunnableRuntimeAndClosesStartedWorkers(t *testing.T) {
	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	if runtime.store == nil || runtime.records == nil || runtime.identityBinding == nil {
		t.Fatal("runtime composition is incomplete")
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
	runtime := NewWithBusinessHandlers(t.Context(), bootstrapTestConfig(t), handlers, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
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
		runtimeIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), nil,
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

func TestProjectRuntimeJoinsSharedReleaseCohortBeforeServingTraffic(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "runtime-release-a"
	identity := bootstrapRuntimeReleaseIdentity(t, '1')
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	first := NewProjectWithIdentity(t.Context(), cfg, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	t.Cleanup(func() { _ = first.CloseContext(t.Context()) })
	firstLease := first.runtimeReleaseLease()
	if firstLease.InstanceID != cfg.RuntimeInstanceID || firstLease.Generation == 0 || first.releaseAdmission.Check() != nil {
		t.Fatalf("first release lease=%+v admission=%v", firstLease, first.releaseAdmission.Check())
	}

	matchingConfig := cfg
	matchingConfig.RuntimeInstanceID = "runtime-release-b"
	matching := NewProjectWithIdentity(t.Context(), matchingConfig, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	matchingLease := matching.runtimeReleaseLease()
	if matchingLease.Generation != firstLease.Generation {
		t.Fatalf("matching generations first=%d second=%d", firstLease.Generation, matchingLease.Generation)
	}
	if err := matching.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	conflicting := identity
	conflicting.MetadataSnapshotSHA256 = strings.Repeat("2", 64)
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
	NewProjectWithIdentity(t.Context(), conflictConfig, handlers, connectors, conflicting, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
}

func bootstrapRuntimeReleaseIdentity(t *testing.T, marker byte) deploymentmodel.RuntimeReleaseIdentity {
	t.Helper()
	hash := func(value byte) string { return strings.Repeat(string(value), 64) }
	identity := deploymentmodel.RuntimeReleaseIdentity{
		ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion, BuildMode: "development", RuntimeVersion: "runtime-test",
		RuntimeextContractVersion: "runtimeext-test", RuntimeextContractSHA256: hash('a'), ConnectorContractVersion: "connector-test", ConnectorContractSHA256: hash('b'),
		DomainSDKContractVersion: "sdk-test", DomainSDKContractSHA256: hash('c'), DomainSDKGeneratorVersion: "generator-test", DomainSDKBuildConstraint: "constraint-test",
		MetadataSnapshotSHA256: hash(marker), GeneratedSDKSHA256: hash('d'), HandlerRegistrySHA256: hash('e'), ConnectorRegistrySHA256: hash('f'),
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
	runtime := NewProjectWithIdentity(t.Context(), cfg, handlers, connectors, identity, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	t.Cleanup(func() { _ = runtime.CloseContext(t.Context()) })
	if err := runtime.releaseIntegrity.SchemaReadiness(t.Context()); err != nil {
		t.Fatalf("initial Schema readiness=%v", err)
	}
	if err := runtime.releaseIntegrity.RegistryReadiness(t.Context()); err != nil {
		t.Fatalf("initial Registry readiness=%v", err)
	}
	if _, err := runtime.store.DB().ExecContext(t.Context(), "UPDATE metadata_catalog SET value = ? WHERE key = ?", "drifted-schema", "schema_hash"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.releaseIntegrity.SchemaReadiness(t.Context()); !errors.Is(err, deploymentapplication.ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("drifted Schema readiness=%v", err)
	}
}

func TestNewBuildsObjectlessConfiguringRuntimeForDirectAuthoring(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.ManifestPath = writeManifestLoaderFixture(t, `{"schema_version":"2","template_id":"direct-authoring-project","version":"0.0.0-configuring","source_blueprint_id":"runtime-direct-authoring-v4","objects":[],"views":[]}`)
	cfg.AllowEmptyAuthoringManifest = true
	cfg.RuntimeAllowDevIdentityHeaders = true
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
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
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	defer runtime.CloseContext(t.Context())
	permissions := []string{
		"workspace.admin", "scheduler.definition.read", "ops.workflow.read", "workflow.process.read",
		"integration.audit.view", "integration.catalog.view",
		"job_definition.read", "job_run.read", "job_dead_letter.read",
	}
	dataPermissions := []accessfixture.DataPolicyFixture{
		{ObjectKey: "job_definition", Scope: "all_records", Read: true},
		{ObjectKey: "job_run", Scope: "all_records", Read: true},
		{ObjectKey: "job_dead_letter", Scope: "all_records", Read: true},
	}
	for _, object := range canonical.Objects {
		permissions = append(permissions, object.Key+".read")
		dataPermissions = append(dataPermissions, accessfixture.DataPolicyFixture{ObjectKey: object.Key, Scope: "all_records", Read: true})
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: permissions, DataPolicies: dataPermissions})
	for _, objectKey := range []string{"job_definition", "job_run", "job_dead_letter"} {
		if !admin.Allows(objectKey, "read") {
			t.Fatalf("test principal lacks %s.read: permissions=%#v bundle=%#v", objectKey, admin.PermissionKeys(), admin.AccessBundle)
		}
	}
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
		"seed_records", "frontend_capabilities",
	} {
		if checks["configuration."+category] != "ok" {
			t.Fatalf("global validation did not cover %s: %#v", category, report)
		}
	}
	for _, check := range []string{"cross_resource_references", "cycles", "permission_closure", "foundation_usage", "connector_readiness", "seed_writability", "frontend_support"} {
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
	ctx := requestcontext.WithRuntimeAuthoringBuilderTaskID(request.Context(), "builder-task-e2e")
	ctx = requestcontext.WithWorkspaceID(ctx, "default")
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Builder-Task-ID", "builder-task-e2e")
	request.Header.Set("X-Domainry-Product-Surface", "admin_console")
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
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
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
	assertBootstrapPanic(t, func() { New(nil, config.Config{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory()) })
	assertBootstrapPanic(t, func() {
		New(t.Context(), config.Config{
			Environment:                    "production",
			RuntimeAllowDevIdentityHeaders: true,
		}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	})
	assertBootstrapPanic(t, func() {
		New(t.Context(), config.Config{ManifestPath: filepath.Join(t.TempDir(), "missing.json")}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	})
	assertBootstrapPanic(t, func() {
		cfg := bootstrapTestConfig(t)
		cfg.DatabaseDriver = "unsupported"
		New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	})
}

func TestNewPropagatesIntegrationNotificationAndServiceAssemblyFailures(t *testing.T) {
	unknownConnection := bootstrapTestConfig(t)
	unknownConnection.ManifestPath = writeManifestLoaderFixture(t, `{
		"schema_version":"2",
		"objects":[{"key":"account","fields":[{"key":"name","type":"text"}]}],
		"integrations":{"connections":[{"key":"bad","connector_key":"missing","provider_key":"missing"}]}
	}`)
	unknownConnection.SkipManifestValidation = true
	assertBootstrapPanic(t, func() {
		New(t.Context(), unknownConnection, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	})

	invalidNotification := bootstrapTestConfig(t)
	invalidNotification.ManifestPath = writeManifestLoaderFixture(t, `{
		"schema_version":"2",
		"objects":[{"key":"account","fields":[{"key":"name","type":"text"}]}],
		"notification_templates":[{"key":"invalid","channel":"email","status":"published","version":1,"default_locale":"en-US","locales":{"en-US":{"subject":"Test","text":"Test"}}}]
	}`)
	invalidNotification.SkipManifestValidation = true
	assertBootstrapPanic(t, func() {
		New(t.Context(), invalidNotification, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	})

	missingFrontend := bootstrapTestConfig(t)
	missingFrontend.FrontendCapabilityManifestPath = filepath.Join(t.TempDir(), "missing-frontend-capability.json")
	assertBootstrapPanic(t, func() {
		New(t.Context(), missingFrontend, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
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
	withCatalog, err := addRuntimeConnectorValidationCatalog(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addRuntimeConnectorValidationCatalog(withCatalog); err != nil {
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
	if _, err := mergeRuntimeConnectorValidationCatalog(manifestmodel.ManifestSchema{}, nil, errors.New("catalog unavailable")); err == nil {
		t.Fatal("catalog failure must propagate")
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

func TestRuntimeWiresCredentialExpirySourceToIdempotentNotificationPublisher(t *testing.T) {
	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	t.Cleanup(func() { _ = runtime.CloseContext(t.Context()) })
	now := runtime.worker.Clock.Now().UTC()
	configStore := integrationpersistence.NewIntegrationConfigStore(runtime.store)
	if _, err := configStore.UpsertSecret(t.Context(), runtime.cfg.NotificationWorkspaceID, integrationmodel.IntegrationSecret{
		Key: "erp-token", WorkspaceID: runtime.cfg.NotificationWorkspaceID, Kind: "api_key", Status: "active", Description: "ERP token",
		CreatedBy: "credential-owner", ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test wired credential expiry notification")
	for attempt := 0; attempt < 2; attempt++ {
		count, err := runtime.records.Applications().Integrations.ProcessCredentialExpiryNotifications(t.Context(), now, 100, scope)
		if err != nil {
			t.Fatal(err)
		}
		if want := 1 - attempt; count != want {
			t.Fatalf("attempt=%d published=%d want=%d", attempt, count, want)
		}
	}
	var events int
	if err := runtime.store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+runtime.store.TableIdentifier("notification_events")+" WHERE "+runtime.store.Identifier("workspace_id")+" = "+runtime.store.Placeholder(1)+" AND "+runtime.store.Identifier("source_event_id")+" LIKE "+runtime.store.Placeholder(2), runtime.cfg.NotificationWorkspaceID, "credential_expiry:erp-token:%").Scan(&events); err != nil || events != 1 {
		t.Fatalf("events=%d err=%v", events, err)
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
