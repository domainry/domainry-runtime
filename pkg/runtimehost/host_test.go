package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/telemetry"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type serverRuntimeFake struct {
	gateway        runtimeConnectorGateway
	startedHook    func()
	started        int
	closed         int
	listenerGroups []runtimehttp.ListenerRouteGroup
}

type identityFactoryStub struct {
	binding identitysdk.Binding
	err     error
}

type agentFactoryStub struct{}

type integrationFactoryStub struct{}

func (integrationFactoryStub) DeploymentMode() integrationsdk.DeploymentMode {
	return integrationsdk.DeploymentModeModule
}

func (agentFactoryStub) Open(context.Context, agentsdk.ApplicationRef) (agentsdk.Binding, error) {
	return nil, errors.New("unused Agent factory")
}

func (factory identityFactoryStub) Open(context.Context, identitysdk.ApplicationRef) (identitysdk.Binding, error) {
	if factory.err != nil {
		return nil, factory.err
	}
	if factory.binding != nil {
		return factory.binding, nil
	}
	return identityBindingStub{}, nil
}

func (factory identityFactoryStub) OpenBootstrapWithDatabase(context.Context, identitysdk.ApplicationKey, identitysdk.DatabaseHandle) (identitysdk.BootstrapBinding, error) {
	if factory.err != nil {
		return nil, factory.err
	}
	return identityBootstrapBindingStub{}, nil
}

type identityBootstrapBindingStub struct{}

func (identityBootstrapBindingStub) ProvisionWorkspaceIdentity(_ context.Context, request identitysdk.WorkspaceIdentityProvisionRequest, _ identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceIdentityProvisionResult, error) {
	return identitysdk.WorkspaceIdentityProvisionResult{AdminLoginID: request.AdminLoginID, InitialPassword: request.InitialPassword, MustChangePassword: true, ProvisionedRoles: 1}, nil
}

func (identityBootstrapBindingStub) ReconcileWorkspaceRoles(context.Context, identitysdk.WorkspaceRoleReconcileRequest, identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceRoleReconcileResult, error) {
	return identitysdk.WorkspaceRoleReconcileResult{}, nil
}

func (identityBootstrapBindingStub) Close(context.Context) error { return nil }

type identityBindingStub struct {
	runtimetestkit.IdentityBindingStub
}

type notificationFactoryStub struct{}

func (notificationFactoryStub) Open(context.Context, notificationsdk.ApplicationRef) (notificationsdk.Binding, error) {
	return nil, nil
}

type partyFactoryStub struct{}

func (partyFactoryStub) Open(context.Context, partysdk.ApplicationRef) (partysdk.Binding, error) {
	return nil, nil
}

type monitoringFactoryStub struct{}

func (monitoringFactoryStub) Open(context.Context, monitoringsdk.ApplicationRef) (monitoringsdk.Binding, error) {
	return nil, nil
}

type schedulerFactoryStub struct{}

func (schedulerFactoryStub) Open(context.Context, schedulersdk.ApplicationRef) (schedulersdk.Binding, error) {
	return nil, nil
}

type dataExchangeFactoryStub struct{}

func (dataExchangeFactoryStub) Open(context.Context, dataexchangesdk.ApplicationRef) (dataexchangesdk.Binding, error) {
	return nil, nil
}

func (identityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeSaaS}
}

type serverConnectorGatewayFake struct{}

func (serverConnectorGatewayFake) Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error) {
	return ConnectorCallResult{}, nil
}

func (f *serverRuntimeFake) StartWorkers(context.Context) {
	f.started++
	if f.startedHook != nil {
		f.startedHook()
	}
}
func (f *serverRuntimeFake) Routes() http.Handler { return http.NotFoundHandler() }
func (f *serverRuntimeFake) RoutesForListenerGroup(group runtimehttp.ListenerRouteGroup) http.Handler {
	f.listenerGroups = append(f.listenerGroups, group)
	return http.NotFoundHandler()
}
func (f *serverRuntimeFake) connectorGateway() runtimeConnectorGateway {
	if f.gateway != nil {
		return f.gateway
	}
	return serverConnectorGatewayFake{}
}
func (f *serverRuntimeFake) CloseContext(context.Context) error { f.closed++; return nil }

func validOptions() Options {
	domainSDK := DomainSDKIdentity{
		ContractVersion: "runtime-domain-sdk-v1", ContractSHA256: strings.Repeat("a", 64), GeneratorVersion: "domaincodegen-v1",
		ApplicationSchemaSnapshotSHA256: strings.Repeat("b", 64), RuntimeextContractSHA256: runtimeext.ContractSHA256, ArtifactSHA256: strings.Repeat("c", 64),
	}
	domainSDK.BuildConstraint = domainSDKBuildConstraint(domainSDK)
	return Options{Identity: BuildIdentity{
		RuntimeVersion:            "test-version",
		RuntimeextContractVersion: runtimeext.ContractVersion,
		RuntimeextContractSHA256:  runtimeext.ContractSHA256,
		ConnectorContractVersion:  connector.ContractVersion,
		ConnectorContractSHA256:   connector.ContractSHA256,
		DomainSDK:                 domainSDK,
	}, IdentityFactory: identityFactoryStub{}, NotificationFactory: notificationFactoryStub{}, PartyFactory: partyFactoryStub{}, MonitoringFactory: monitoringFactoryStub{}, SchedulerFactory: schedulerFactoryStub{}, DataExchangeFactory: dataExchangeFactoryStub{}, AgentFactory: agentFactoryStub{}, IntegrationFactory: integrationFactoryStub{}, ReportFactory: reportmodule.NewFactory()}
}

func serverManifestJSON(t *testing.T, target *manifestmodel.GeneratedDomainSDKIdentity) []byte {
	t.Helper()
	manifest := manifestmodel.ManifestSchema{
		SchemaVersion: manifestmodel.CurrentManifestSchemaVersion, TemplateID: "server-test", Version: "1.0.0", Name: "Server Test",
		SourceBlueprintID: provision.DirectAuthoringSourceID, Objects: []definitionmodel.ObjectSchema{}, GeneratedDomainSDK: target,
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func manifestDomainSDKTarget(identity DomainSDKIdentity) *manifestmodel.GeneratedDomainSDKIdentity {
	return &manifestmodel.GeneratedDomainSDKIdentity{
		ContractVersion: identity.ContractVersion, ContractSHA256: identity.ContractSHA256, GeneratorVersion: identity.GeneratorVersion,
		ApplicationSchemaSnapshotSHA256: identity.ApplicationSchemaSnapshotSHA256, RuntimeextContractSHA256: identity.RuntimeextContractSHA256,
		BuildConstraint: identity.BuildConstraint, ArtifactSHA256: identity.ArtifactSHA256,
	}
}

func serverTestConfig() config.Config {
	return config.Config{
		Port: ":0", ManifestPath: "runtime-manifest.json", TelemetryExportTimeout: time.Second,
		IdentityAudience: "domainry-runtime", InitialTenantRequestID: "initial-tenant", InitialTenantCode: "primary",
		InitialTenantName: "Primary", InitialTenantAdminLoginID: "admin@example.test", InitialTenantAdminName: "Admin",
		InitialTenantAdminPassword: "BootstrapAdmin1!", InitialTenantStoreConfiguration: "{}",
		HTTPReadHeaderTimeout: time.Second, HTTPReadTimeout: time.Second, HTTPWriteTimeout: time.Second,
		HTTPIdleTimeout: time.Second, HTTPShutdownTimeout: time.Second, HTTPMaxHeaderBytes: 1024,
	}
}

func serverTestDependencies(t *testing.T, cfg config.Config, runtime runtimeProcess) serverRunDependencies {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "runtimehost.db")
	return serverRunDependencies{
		loadConfig: func() (config.Config, config.Snapshot, error) {
			return cfg, config.Snapshot{
				Revision: "revision-1",
				Entries:  map[string]config.Provenance{"PORT": {Name: "PORT", Source: "test", Version: "1"}},
				Warnings: []string{"test warning"},
			}, nil
		},
		notifyContext: func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		},
		initializeTracer: func(context.Context, telemetry.Config) (telemetry.Shutdown, error) {
			return func(context.Context) error { return nil }, nil
		},
		stat: func(string) (os.FileInfo, error) { return nil, nil },
		readFile: func(string) ([]byte, error) {
			return serverManifestJSON(t, manifestDomainSDKTarget(validOptions().Identity.DomainSDK)), nil
		},
		prepareDatabase: func(ctx context.Context, databaseConfig config.Config) (*bootstrap.ProjectDatabase, error) {
			databaseConfig.DatabaseDriver = "sqlite"
			databaseConfig.DBPath = databasePath
			return bootstrap.PrepareProjectDatabase(ctx, databaseConfig)
		},
		newRuntime: func(_ context.Context, _ config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity runtimehttp.RuntimeReleaseIdentity, evidence bootstrap.RuntimeReleaseArtifactEvidence, _ identitysdk.Binding, _ notificationsdk.Factory, _ partysdk.Factory, _ monitoringsdk.Factory, _ schedulersdk.Factory, _ dataexchangesdk.Factory, _ agentsdk.Factory, _ integrationsdk.Factory, _ reportsdk.Factory, _ *bootstrap.ProjectDatabase) runtimeProcess {
			if handlers == nil || !handlers.Frozen() {
				panic("host passed an unfrozen registry")
			}
			if connectors == nil || !connectors.Frozen() {
				panic("host passed an unfrozen connector registry")
			}
			if identity.ContractVersion != RuntimeReleaseIdentityVersion || identity.CombinationSHA256 == "" {
				panic("host omitted Runtime release identity")
			}
			if !evidence.Verified || evidence.BuildError != nil || evidence.SignatureError != nil {
				panic("host omitted successful development artifact evidence")
			}
			return runtime
		},
		listenAndServe: func(*http.Server) error { return http.ErrServerClosed },
		shutdown:       func(context.Context, *http.Server) error { return nil },
	}
}

func TestBuildIdentityFailsClosed(t *testing.T) {
	valid := validOptions().Identity
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid identity: %v", err)
	}
	generic := valid
	generic.DomainSDK = DomainSDKIdentity{}
	if err := generic.Validate(); err != nil {
		t.Fatalf("generic Runtime identity: %v", err)
	}
	invalid := []BuildIdentity{
		{},
		{RuntimeVersion: "test", RuntimeextContractVersion: "stale", RuntimeextContractSHA256: runtimeext.ContractSHA256, ConnectorContractVersion: connector.ContractVersion, ConnectorContractSHA256: connector.ContractSHA256},
		{RuntimeVersion: "test", RuntimeextContractVersion: runtimeext.ContractVersion, RuntimeextContractSHA256: "stale", ConnectorContractVersion: connector.ContractVersion, ConnectorContractSHA256: connector.ContractSHA256},
		{RuntimeVersion: "test", RuntimeextContractVersion: runtimeext.ContractVersion, RuntimeextContractSHA256: runtimeext.ContractSHA256, ConnectorContractVersion: "stale", ConnectorContractSHA256: connector.ContractSHA256},
		{RuntimeVersion: "test", RuntimeextContractVersion: runtimeext.ContractVersion, RuntimeextContractSHA256: runtimeext.ContractSHA256, ConnectorContractVersion: connector.ContractVersion, ConnectorContractSHA256: "stale"},
	}
	for _, identity := range invalid {
		if err := identity.Validate(); err == nil {
			t.Fatalf("identity unexpectedly valid: %#v", identity)
		}
	}
}

func TestDomainSDKIdentityFailsClosed(t *testing.T) {
	valid := validOptions().Identity.DomainSDK
	cases := map[string]struct {
		identity DomainSDKIdentity
		want     error
	}{
		"contract required":  {identity: func() DomainSDKIdentity { value := valid; value.ContractVersion = ""; return value }(), want: ErrDomainSDKContractRequired},
		"generator required": {identity: func() DomainSDKIdentity { value := valid; value.GeneratorVersion = ""; return value }(), want: ErrDomainSDKGeneratorRequired},
		"hash invalid":       {identity: func() DomainSDKIdentity { value := valid; value.ArtifactSHA256 = "stale"; return value }(), want: ErrDomainSDKHashInvalid},
		"artifact stale":     {identity: func() DomainSDKIdentity { value := valid; value.ArtifactSHA256 = strings.Repeat("d", 64); return value }(), want: ErrDomainSDKBuildMismatch},
		"runtimeext stale": {identity: func() DomainSDKIdentity {
			value := valid
			value.RuntimeextContractSHA256 = strings.Repeat("d", 64)
			value.BuildConstraint = domainSDKBuildConstraint(value)
			return value
		}(), want: ErrDomainSDKRuntimeextMismatch},
		"build stale": {identity: func() DomainSDKIdentity {
			value := valid
			value.BuildConstraint = "domainry_domain_sdk_stale"
			return value
		}(), want: ErrDomainSDKBuildMismatch},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if err := test.identity.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v identity=%#v", err, test.want, test.identity)
			}
		})
	}
}

func TestValidateDomainSDKTargetRequiresExactIdentity(t *testing.T) {
	compiled := validOptions().Identity.DomainSDK
	if err := validateDomainSDKTarget(DomainSDKIdentity{}, nil); err != nil {
		t.Fatalf("generic Runtime without generated target: %v", err)
	}
	if err := validateDomainSDKTarget(DomainSDKIdentity{}, manifestDomainSDKTarget(compiled)); !errors.Is(err, ErrDomainSDKContractRequired) {
		t.Fatalf("target without compiled SDK error=%v", err)
	}
	if err := validateDomainSDKTarget(compiled, manifestDomainSDKTarget(compiled)); err != nil {
		t.Fatalf("matching target: %v", err)
	}
	if err := validateDomainSDKTarget(compiled, nil); !errors.Is(err, ErrDomainSDKTargetRequired) {
		t.Fatalf("missing target error=%v", err)
	}
	mutations := map[string]func(*manifestmodel.GeneratedDomainSDKIdentity){
		"contract_version": func(target *manifestmodel.GeneratedDomainSDKIdentity) { target.ContractVersion = "stale" },
		"contract_sha256": func(target *manifestmodel.GeneratedDomainSDKIdentity) {
			target.ContractSHA256 = strings.Repeat("d", 64)
		},
		"generator_version": func(target *manifestmodel.GeneratedDomainSDKIdentity) { target.GeneratorVersion = "stale" },
		"metadata_snapshot_sha256": func(target *manifestmodel.GeneratedDomainSDKIdentity) {
			target.ApplicationSchemaSnapshotSHA256 = strings.Repeat("d", 64)
		},
		"runtimeext_contract_sha256": func(target *manifestmodel.GeneratedDomainSDKIdentity) {
			target.RuntimeextContractSHA256 = strings.Repeat("d", 64)
		},
		"build_constraint": func(target *manifestmodel.GeneratedDomainSDKIdentity) {
			target.BuildConstraint = "domainry_domain_sdk_stale"
		},
		"artifact_sha256": func(target *manifestmodel.GeneratedDomainSDKIdentity) {
			target.ArtifactSHA256 = strings.Repeat("d", 64)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			target := manifestDomainSDKTarget(compiled)
			mutate(target)
			if err := validateDomainSDKTarget(compiled, target); !errors.Is(err, ErrDomainSDKTargetMismatch) || !strings.Contains(err.Error(), "field="+name) {
				t.Fatalf("mismatch error=%v", err)
			}
		})
	}
}

func TestPrepareBusinessHandlersFreezesRegistry(t *testing.T) {
	registry, gateway, err := prepareBusinessHandlers(validOptions())
	if err != nil || !registry.Frozen() || len(registry.Descriptors()) != 0 || gateway == nil {
		t.Fatalf("registry=%#v gateway=%#v error=%v", registry, gateway, err)
	}
}

func TestPrepareConnectorProvidersFreezesRegistry(t *testing.T) {
	registry, err := prepareConnectorProviders(validOptions())
	if err != nil || !registry.Frozen() || len(registry.Descriptors()) != 0 {
		t.Fatalf("registry=%#v error=%v", registry, err)
	}
}

func TestRunWithDependenciesRejectsStaleCompositionBeforeRuntimeStartup(t *testing.T) {
	options := validOptions()
	options.Identity.RuntimeextContractSHA256 = "stale"
	dependencies := serverRunDependencies{
		loadConfig: func() (config.Config, config.Snapshot, error) {
			t.Fatal("configuration must not load for stale generated composition")
			return config.Config{}, config.Snapshot{}, nil
		},
	}
	err := runWithDependencies(options, dependencies)
	if !errors.Is(err, ErrRuntimeextHashMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunWithDependenciesRejectsPackagedRuntimeWithoutValidAttestationBeforeConfiguration(t *testing.T) {
	restore := setRuntimeReleaseLinkerFactsForTest()
	defer restore()
	dependencies := serverRunDependencies{
		executable: func() (string, error) { return "/missing/domainry-runtime", nil },
		readFile:   func(string) ([]byte, error) { return nil, errors.New("attestation missing") },
		loadConfig: func() (config.Config, config.Snapshot, error) {
			t.Fatal("configuration must not load before packaged artifact verification")
			return config.Config{}, config.Snapshot{}, nil
		},
	}
	err := runWithDependencies(validOptions(), dependencies)
	if !errors.Is(err, ErrRuntimeArtifactAttestation) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunWithDependenciesBindsAndUnbindsGeneratedConnectorGateway(t *testing.T) {
	options := validOptions()
	var generated ConnectorGateway
	options.BusinessHandlers = func(gateway ConnectorGateway) (runtimeext.ExtensionSet, error) {
		generated = gateway
		return runtimeext.ExtensionSet{}, nil
	}
	target := &connectorBindingTarget{result: ConnectorCallResult{Payload: json.RawMessage(`{"name":"Ada"}`)}}
	runtime := &serverRuntimeFake{gateway: target}
	runtime.startedHook = func() {
		result, err := generated.Call(t.Context(), connectorBindingExecution{}, ConnectorCallRequest{ConnectorKey: "member_center", ConnectionKey: "primary", OperationKey: "get_member", ContractSHA256: "contract", Mode: "call", Payload: json.RawMessage(`{}`)})
		if err != nil || string(result.Payload) != `{"name":"Ada"}` {
			t.Fatalf("bound gateway result=%s error=%v", result.Payload, err)
		}
	}
	if err := runWithDependencies(options, serverTestDependencies(t, serverTestConfig(), runtime)); err != nil {
		t.Fatal(err)
	}
	if _, err := generated.Call(t.Context(), connectorBindingExecution{}, ConnectorCallRequest{Payload: json.RawMessage(`{}`)}); runtimeextErrorCode(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("gateway remained bound after Runtime close: %v", err)
	}
}

func TestRunWithDependenciesRejectsManifestSDKTargetBeforeRuntimeCreation(t *testing.T) {
	options := validOptions()
	cases := map[string]struct {
		target  *manifestmodel.GeneratedDomainSDKIdentity
		wantErr error
	}{
		"missing": {wantErr: ErrDomainSDKTargetRequired},
		"stale": {
			target: func() *manifestmodel.GeneratedDomainSDKIdentity {
				target := manifestDomainSDKTarget(options.Identity.DomainSDK)
				target.ArtifactSHA256 = strings.Repeat("d", 64)
				return target
			}(),
			wantErr: ErrDomainSDKTargetMismatch,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			created := 0
			deps := serverTestDependencies(t, serverTestConfig(), &serverRuntimeFake{})
			deps.readFile = func(string) ([]byte, error) { return serverManifestJSON(t, test.target), nil }
			deps.newRuntime = func(context.Context, config.Config, *runtimeext.BusinessHandlerRegistry, *connector.Registry, runtimehttp.RuntimeReleaseIdentity, bootstrap.RuntimeReleaseArtifactEvidence, identitysdk.Binding, notificationsdk.Factory, partysdk.Factory, monitoringsdk.Factory, schedulersdk.Factory, dataexchangesdk.Factory, agentsdk.Factory, integrationsdk.Factory, reportsdk.Factory, *bootstrap.ProjectDatabase) runtimeProcess {
				created++
				return &serverRuntimeFake{}
			}
			if err := runWithDependencies(options, deps); !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v", err)
			}
			if created != 0 {
				t.Fatalf("Runtime was created %d times before SDK target validation", created)
			}
		})
	}
}

func TestRuntimeActivatorCoversSuccessDuplicateFailurePanicAndClose(t *testing.T) {
	handler := &bootstrap.EntrypointMux{}
	runtime := &serverRuntimeFake{}
	created := 0
	activator := &runtimeActivator{handler: handler, close: func(runtime runtimeProcess) error {
		return runtime.CloseContext(t.Context())
	}, start: func(manifest manifestmodel.ManifestSchema) (runtimeProcess, error) {
		created++
		runtime.StartWorkers(t.Context())
		return runtime, nil
	}}
	err := activator.Activate(manifestmodel.ManifestSchema{})
	if err != nil || created != 1 || runtime.started != 1 {
		t.Fatalf("created=%d runtime=%#v error=%v", created, runtime, err)
	}
	if err := activator.Activate(manifestmodel.ManifestSchema{}); err != nil || created != 1 {
		t.Fatalf("duplicate activation created=%d error=%v", created, err)
	}
	activator.Close()
	if runtime.closed != 1 {
		t.Fatalf("close count=%d", runtime.closed)
	}

	startFailure := errors.New("start failed")
	failed := &runtimeActivator{handler: handler, start: func(manifestmodel.ManifestSchema) (runtimeProcess, error) {
		return nil, startFailure
	}}
	if err := failed.Activate(manifestmodel.ManifestSchema{}); !errors.Is(err, startFailure) || failed.runtime != nil {
		t.Fatalf("start failure runtime=%#v error=%v", failed.runtime, err)
	}

	panicking := &runtimeActivator{handler: handler, start: func(manifestmodel.ManifestSchema) (runtimeProcess, error) {
		panic("bootstrap panic")
	}}
	if err := panicking.Activate(manifestmodel.ManifestSchema{}); err == nil || !strings.Contains(err.Error(), "bootstrap panic") {
		t.Fatalf("panic error=%v", err)
	}
}

func TestRunWithDependenciesCoversConfigurationActivationAndServeOutcomes(t *testing.T) {
	options := validOptions()
	cfg := serverTestConfig()
	runtime := &serverRuntimeFake{}
	deps := serverTestDependencies(t, cfg, runtime)
	if err := runWithDependencies(options, deps); err != nil || runtime.started != 1 || runtime.closed != 1 {
		t.Fatalf("successful run runtime=%#v error=%v", runtime, err)
	}

	loadErr := errors.New("load failed")
	deps = serverTestDependencies(t, cfg, runtime)
	deps.loadConfig = func() (config.Config, config.Snapshot, error) { return config.Config{}, config.Snapshot{}, loadErr }
	if err := runWithDependencies(options, deps); err == nil || !strings.Contains(err.Error(), "load Runtime configuration") {
		t.Fatalf("load error=%v", err)
	}

	listenErr := errors.New("listen failed")
	deps = serverTestDependencies(t, cfg, runtime)
	deps.stat = func(string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: cfg.ManifestPath, Err: os.ErrNotExist}
	}
	deps.listenAndServe = func(*http.Server) error { return listenErr }
	if err := runWithDependencies(options, deps); !errors.Is(err, listenErr) {
		t.Fatalf("listen error=%v", err)
	}

	statErr := errors.New("stat failed")
	deps = serverTestDependencies(t, cfg, runtime)
	deps.stat = func(string) (os.FileInfo, error) { return nil, statErr }
	if err := runWithDependencies(options, deps); !errors.Is(err, statErr) {
		t.Fatalf("stat error=%v", err)
	}

	readErr := errors.New("read failed")
	deps = serverTestDependencies(t, cfg, runtime)
	deps.readFile = func(string) ([]byte, error) { return nil, readErr }
	if err := runWithDependencies(options, deps); !errors.Is(err, readErr) || !strings.Contains(err.Error(), "read Runtime manifest") {
		t.Fatalf("read error=%v", err)
	}

	deps = serverTestDependencies(t, cfg, runtime)
	deps.readFile = func(string) ([]byte, error) { return []byte(`{"schema_version":"2","unknown":true}`), nil }
	if err := runWithDependencies(options, deps); err == nil || !strings.Contains(err.Error(), "decode Runtime manifest") {
		t.Fatalf("decode error=%v", err)
	}

	var generated ConnectorGateway
	options.BusinessHandlers = func(gateway ConnectorGateway) (runtimeext.ExtensionSet, error) {
		generated = gateway
		return runtimeext.ExtensionSet{}, nil
	}
	deps = serverTestDependencies(t, cfg, &serverRuntimeFake{})
	deps.newRuntime = func(context.Context, config.Config, *runtimeext.BusinessHandlerRegistry, *connector.Registry, runtimehttp.RuntimeReleaseIdentity, bootstrap.RuntimeReleaseArtifactEvidence, identitysdk.Binding, notificationsdk.Factory, partysdk.Factory, monitoringsdk.Factory, schedulersdk.Factory, dataexchangesdk.Factory, agentsdk.Factory, integrationsdk.Factory, reportsdk.Factory, *bootstrap.ProjectDatabase) runtimeProcess {
		return nil
	}
	if err := runWithDependencies(options, deps); err == nil || !strings.Contains(err.Error(), "returned no process") {
		t.Fatalf("activation error=%v", err)
	}
	if _, err := generated.Call(t.Context(), connectorBindingExecution{}, ConnectorCallRequest{Payload: json.RawMessage(`{}`)}); runtimeextErrorCode(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("failed Runtime gateway remained bound: %v", err)
	}
}

func TestRunWithDependenciesLoadsProjectConfigAndI18nExtensions(t *testing.T) {
	options := validOptions()
	options.ProjectConfigFile = "config/runtime.json"
	options.ProjectI18nDir = "i18n"
	cfg := serverTestConfig()
	runtime := &serverRuntimeFake{}
	deps := serverTestDependencies(t, cfg, runtime)
	var configPath, i18nPath string
	deps.loadConfig = func() (config.Config, config.Snapshot, error) {
		t.Fatal("plain Runtime configuration loader used for project extension")
		return config.Config{}, config.Snapshot{}, nil
	}
	deps.loadProjectConfig = func(path string) (config.Config, config.Snapshot, error) {
		configPath = path
		return cfg, config.Snapshot{Revision: "project-config", Entries: map[string]config.Provenance{}}, nil
	}
	deps.configureProjectI18n = func(path string) error {
		i18nPath = path
		return nil
	}
	if err := runWithDependencies(options, deps); err != nil {
		t.Fatal(err)
	}
	if configPath != options.ProjectConfigFile || i18nPath != options.ProjectI18nDir {
		t.Fatalf("project extensions config=%q i18n=%q", configPath, i18nPath)
	}

	deps = serverTestDependencies(t, cfg, runtime)
	deps.configureProjectI18n = func(string) error { return errors.New("invalid project locale") }
	if err := runWithDependencies(options, deps); err == nil || !strings.Contains(err.Error(), "configure project i18n extension") {
		t.Fatalf("i18n error=%v", err)
	}
}

func TestRunWithDependenciesBootstrapsQGSizedProjectI18nExtension(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeHostCatalogFixture(t, filepath.Join(dir, "pt-BR.toml"), 267873, 3572)
	t.Cleanup(func() { _ = localization.ConfigureProjectExtension("") })

	options := validOptions()
	options.ProjectI18nDir = dir
	cfg := serverTestConfig()
	runtime := &serverRuntimeFake{}
	deps := serverTestDependencies(t, cfg, runtime)
	deps.configureProjectI18n = localization.ConfigureProjectExtension
	if err := runWithDependencies(options, deps); err != nil {
		t.Fatal(err)
	}
	if value, ok := localization.Lookup("pt-BR", "project.capacity.key_3571"); !ok || value != "value 3571" {
		t.Fatalf("qg-sized project locale was not active: value=%q ok=%v", value, ok)
	}
}

func writeRuntimeHostCatalogFixture(t *testing.T, path string, size, keyCount int) {
	t.Helper()
	var content strings.Builder
	for index := 0; index < keyCount; index++ {
		_, _ = fmt.Fprintf(&content, `"project.capacity.key_%04d" = "value %04d"`+"\n", index, index)
	}
	padding := size - content.Len()
	if padding < 1 {
		t.Fatalf("catalog fixture content %d exceeds requested size %d", content.Len(), size)
	}
	content.WriteByte('#')
	content.WriteString(strings.Repeat("x", padding-1))
	if content.Len() != size {
		t.Fatalf("catalog fixture size=%d want=%d", content.Len(), size)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithDependenciesStartsThreeIsolatedSurfaceListeners(t *testing.T) {
	cfg := serverTestConfig()
	cfg.HTTPPublicAddr = "127.0.0.1:18081"
	cfg.HTTPTenantAdminAddr = "127.0.0.1:18082"
	cfg.HTTPOpsAddr = "127.0.0.1:18083"
	runtime := &serverRuntimeFake{}
	deps := serverTestDependencies(t, cfg, runtime)
	var mu sync.Mutex
	addresses := []string{}
	allStarted := make(chan struct{})
	var once sync.Once
	deps.listenAndServe = func(server *http.Server) error {
		mu.Lock()
		addresses = append(addresses, server.Addr)
		if len(addresses) == 3 {
			once.Do(func() { close(allStarted) })
		}
		mu.Unlock()
		<-allStarted
		return http.ErrServerClosed
	}
	if err := runWithDependencies(validOptions(), deps); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(addresses)
	if got := strings.Join(addresses, ","); got != "127.0.0.1:18081,127.0.0.1:18082,127.0.0.1:18083" {
		t.Fatalf("listener addresses=%s", got)
	}
	if len(runtime.listenerGroups) != 3 {
		t.Fatalf("surface groups=%v", runtime.listenerGroups)
	}
}

func TestRunWithDependenciesCoversTelemetryAndShutdownOutcomes(t *testing.T) {
	options := validOptions()
	cfg := serverTestConfig()
	deps := serverTestDependencies(t, cfg, &serverRuntimeFake{})
	deps.initializeTracer = func(context.Context, telemetry.Config) (telemetry.Shutdown, error) {
		return nil, errors.New("telemetry unavailable")
	}
	if err := runWithDependencies(options, deps); err != nil {
		t.Fatalf("telemetry fallback error=%v", err)
	}

	telemetryShutdownErr := errors.New("telemetry shutdown failed")
	deps = serverTestDependencies(t, cfg, &serverRuntimeFake{})
	deps.initializeTracer = func(context.Context, telemetry.Config) (telemetry.Shutdown, error) {
		return func(context.Context) error { return telemetryShutdownErr }, nil
	}
	if err := runWithDependencies(options, deps); err != nil {
		t.Fatalf("telemetry shutdown must not fail server: %v", err)
	}

	shutdownErr := errors.New("HTTP shutdown failed")
	failedShutdownRelease := make(chan struct{})
	deps = serverTestDependencies(t, cfg, &serverRuntimeFake{})
	deps.notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	deps.listenAndServe = func(*http.Server) error { <-failedShutdownRelease; return http.ErrServerClosed }
	deps.shutdown = func(context.Context, *http.Server) error { close(failedShutdownRelease); return shutdownErr }
	if err := runWithDependencies(options, deps); !errors.Is(err, shutdownErr) {
		t.Fatalf("shutdown error=%v", err)
	}

	gracefulShutdownRelease := make(chan struct{})
	deps = serverTestDependencies(t, cfg, &serverRuntimeFake{})
	deps.notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	deps.listenAndServe = func(*http.Server) error { <-gracefulShutdownRelease; return http.ErrServerClosed }
	deps.shutdown = func(context.Context, *http.Server) error { close(gracefulShutdownRelease); return nil }
	if err := runWithDependencies(options, deps); err != nil {
		t.Fatalf("graceful shutdown error=%v", err)
	}
}
