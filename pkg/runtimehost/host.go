package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/telemetry"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
	"go.uber.org/zap"
)

// Run validates generated project composition, freezes its Handler registry
// and owns the complete Runtime process lifecycle until shutdown.
func Run(options Options) error {
	logger, err := logging.Initialize("domainry-domain-runtime")
	if err != nil {
		wrapped := fmt.Errorf("initialize logger: %w", err)
		_, _ = fmt.Fprintln(os.Stderr, wrapped)
		return wrapped
	}
	defer func() { _ = logger.Sync() }()
	if err := runWithDependencies(options, defaultServerRunDependencies()); err != nil {
		logger.Error("domain Runtime stopped", logging.StableErrorFields(err)...)
		return err
	}
	return nil
}

type runtimeProcess interface {
	StartWorkers(context.Context)
	Routes() http.Handler
	connectorGateway() runtimeConnectorGateway
	CloseContext(context.Context) error
}

type runtimeSurfaceProcess interface {
	RoutesForSurfaceGroup(runtimehttp.SurfaceRouteGroup) http.Handler
}

type bootstrapRuntimeProcess struct{ *bootstrap.Runtime }

func (r bootstrapRuntimeProcess) StartWorkers(ctx context.Context) {
	bootstrap.StartWorkers(ctx, r.Runtime)
}

func (r bootstrapRuntimeProcess) RoutesForSurfaceGroup(group runtimehttp.SurfaceRouteGroup) http.Handler {
	return bootstrap.RoutesForSurfaceGroup(r.Runtime, group)
}

func (r bootstrapRuntimeProcess) connectorGateway() runtimeConnectorGateway {
	return bootstrap.ActionConnectorGateway(r.Runtime)
}

type serverRunDependencies struct {
	loadConfig           func() (config.Config, config.Snapshot, error)
	loadProjectConfig    func(string) (config.Config, config.Snapshot, error)
	configureProjectI18n func(string) error
	notifyContext        func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)
	initializeTracer     func(context.Context, telemetry.Config) (telemetry.Shutdown, error)
	executable           func() (string, error)
	stat                 func(string) (os.FileInfo, error)
	readFile             func(string) ([]byte, error)
	prepareDatabase      func(context.Context, config.Config) (*bootstrap.ProjectDatabase, error)
	newRuntime           func(context.Context, config.Config, *runtimeext.BusinessHandlerRegistry, *connector.Registry, runtimehttp.RuntimeReleaseIdentity, bootstrap.RuntimeReleaseArtifactEvidence, identitysdk.Binding, notificationsdk.Factory, *bootstrap.ProjectDatabase) runtimeProcess
	listenAndServe       func(*http.Server) error
	shutdown             func(context.Context, *http.Server) error
}

func defaultServerRunDependencies() serverRunDependencies {
	return serverRunDependencies{
		loadConfig:           config.Load,
		loadProjectConfig:    config.LoadWithProjectFile,
		configureProjectI18n: localization.ConfigureProjectExtension,
		notifyContext:        signal.NotifyContext,
		initializeTracer:     telemetry.Initialize,
		executable:           os.Executable,
		stat:                 os.Stat,
		readFile:             os.ReadFile,
		prepareDatabase:      bootstrap.PrepareProjectDatabase,
		newRuntime: func(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity runtimehttp.RuntimeReleaseIdentity, evidence bootstrap.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, database *bootstrap.ProjectDatabase) runtimeProcess {
			return bootstrapRuntimeProcess{Runtime: bootstrap.NewVerifiedProjectWithFactoriesAndDatabase(ctx, cfg, handlers, connectors, identity, evidence, binding, notificationFactory, database)}
		},
		listenAndServe: func(server *http.Server) error { return server.ListenAndServe() },
		shutdown:       func(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) },
	}
}

type runtimeActivator struct {
	handler          *bootstrap.EntrypointMux
	handlers         map[runtimehttp.SurfaceRouteGroup]*bootstrap.EntrypointMux
	connectorGateway *bindableConnectorGateway
	start            func(manifestmodel.ManifestSchema) (runtimeProcess, error)
	close            func(runtimeProcess) error
	mu               sync.Mutex
	runtime          runtimeProcess
}

func (a *runtimeActivator) entrypointHandlers() map[runtimehttp.SurfaceRouteGroup]*bootstrap.EntrypointMux {
	if len(a.handlers) != 0 {
		return a.handlers
	}
	if a.handler != nil {
		return map[runtimehttp.SurfaceRouteGroup]*bootstrap.EntrypointMux{
			runtimehttp.SurfaceRouteGroupAll: a.handler,
		}
	}
	return nil
}

func (a *runtimeActivator) Activate(manifest manifestmodel.ManifestSchema) (err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime != nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Runtime bootstrap failed after manifest Provision: %v", recovered)
		}
	}()
	runtime, startErr := a.start(manifest)
	if startErr != nil {
		return startErr
	}
	a.runtime = runtime
	if surfaceRuntime, ok := runtime.(runtimeSurfaceProcess); ok {
		for group, handler := range a.entrypointHandlers() {
			handler.SetBusiness(surfaceRuntime.RoutesForSurfaceGroup(group))
		}
	} else {
		for _, handler := range a.entrypointHandlers() {
			handler.SetBusiness(runtime.Routes())
		}
	}
	return nil
}

func (a *runtimeActivator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.connectorGateway != nil {
		a.connectorGateway.unbind()
	}
	if a.runtime != nil {
		_ = a.closeRuntime(a.runtime)
	}
}

func (a *runtimeActivator) Abandon() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.connectorGateway != nil {
		a.connectorGateway.unbind()
	}
	if a.runtime != nil {
		if err := a.closeRuntime(a.runtime); err != nil {
			return err
		}
		a.runtime = nil
	}
	for _, handler := range a.entrypointHandlers() {
		handler.SetProvisioning()
	}
	return nil
}

type runtimeHTTPListener struct {
	name  string
	addr  string
	group runtimehttp.SurfaceRouteGroup
}

func runtimeHTTPListeners(cfg config.Config) []runtimeHTTPListener {
	listeners := []runtimeHTTPListener{}
	if strings.TrimSpace(cfg.HTTPPublicAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "public", addr: strings.TrimSpace(cfg.HTTPPublicAddr), group: runtimehttp.SurfaceRouteGroupPublic})
	}
	if strings.TrimSpace(cfg.HTTPTenantAdminAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "tenant-admin", addr: strings.TrimSpace(cfg.HTTPTenantAdminAddr), group: runtimehttp.SurfaceRouteGroupTenantAdmin})
	}
	if strings.TrimSpace(cfg.HTTPOpsAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "ops", addr: strings.TrimSpace(cfg.HTTPOpsAddr), group: runtimehttp.SurfaceRouteGroupOps})
	}
	if len(listeners) == 0 {
		listeners = append(listeners, runtimeHTTPListener{name: "legacy-all", addr: cfg.HTTPAddr(), group: runtimehttp.SurfaceRouteGroupAll})
	}
	return listeners
}

func (a *runtimeActivator) closeRuntime(runtime runtimeProcess) error {
	if a.close != nil {
		return a.close(runtime)
	}
	return fmt.Errorf("Runtime activator close requires its lifecycle callback")
}

func prepareBusinessHandlers(options Options) (*runtimeext.BusinessHandlerRegistry, *bindableConnectorGateway, error) {
	if err := options.Identity.Validate(); err != nil {
		return nil, nil, err
	}
	gateway := &bindableConnectorGateway{}
	extensions := runtimeext.ExtensionSet{}
	if options.BusinessHandlers != nil {
		var err error
		extensions, err = options.BusinessHandlers(gateway)
		if err != nil {
			return nil, nil, fmt.Errorf("build project business handlers: %w", err)
		}
	}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.RegisterExtensionSet(extensions); err != nil {
		return nil, nil, fmt.Errorf("register project extensions: %w", err)
	}
	registry.Freeze()
	return registry, gateway, nil
}

func prepareConnectorProviders(options Options) (*connector.Registry, error) {
	registry := connector.NewRegistry()
	projects := connector.ProviderSet{}
	if options.Connectors != nil {
		var err error
		projects, err = options.Connectors(newConnectorTransportWithProcessPolicy(options.ConnectorProcesses))
		if err != nil {
			return nil, fmt.Errorf("build project Connector Providers: %w", err)
		}
	}
	if err := registry.RegisterProviderSet(projects); err != nil {
		return nil, fmt.Errorf("register project Connector Providers: %w", err)
	}
	registry.Freeze()
	return registry, nil
}

func runWithDependencies(options Options, dependencies serverRunDependencies) error {
	businessHandlers, connectorGateway, err := prepareBusinessHandlers(options)
	if err != nil {
		return fmt.Errorf("validate project Runtime composition: %w", err)
	}
	connectorProviders, err := prepareConnectorProviders(options)
	if err != nil {
		return fmt.Errorf("validate project Runtime composition: %w", err)
	}
	releaseIdentity, err := runtimeReleaseIdentity(options.Identity, businessHandlers, connectorProviders)
	if err != nil {
		return fmt.Errorf("validate project Runtime release identity: %w", err)
	}
	artifactEvidence := bootstrap.RuntimeReleaseArtifactEvidence{Verified: true}
	executablePath := ""
	frontendBundleSHA256 := ""
	if releaseIdentity.BuildMode == "packaged" {
		if dependencies.executable == nil {
			evidenceErr := fmt.Errorf("%w: executable path provider is unavailable", ErrRuntimeArtifactAttestation)
			artifactEvidence.BuildError, artifactEvidence.SignatureError = evidenceErr, evidenceErr
		} else if resolvedExecutablePath, executableErr := dependencies.executable(); executableErr != nil {
			evidenceErr := fmt.Errorf("%w: resolve executable path: %v", ErrRuntimeArtifactAttestation, executableErr)
			artifactEvidence.BuildError, artifactEvidence.SignatureError = evidenceErr, evidenceErr
		} else {
			executablePath = resolvedExecutablePath
			evidence := verifyRuntimeArtifact(executablePath, releaseIdentity, dependencies.readFile)
			artifactEvidence.BuildError, artifactEvidence.SignatureError = evidence.BuildError, evidence.SignatureError
			frontendBundleSHA256 = evidence.FrontendBundleSHA256
		}
		if artifactEvidence.BuildError != nil || artifactEvidence.SignatureError != nil {
			return fmt.Errorf("verify packaged Runtime artifact: %w", errors.Join(artifactEvidence.BuildError, artifactEvidence.SignatureError))
		}
	}
	frontendAssets, err := loadProjectFrontendAssets(executablePath, frontendBundleSHA256, dependencies.readFile)
	if err != nil {
		return fmt.Errorf("load packaged project frontend: %w", err)
	}
	if options.ProjectI18nDir != "" {
		if dependencies.configureProjectI18n == nil {
			return fmt.Errorf("configure project i18n extension: dependency is unavailable")
		}
		if err := dependencies.configureProjectI18n(options.ProjectI18nDir); err != nil {
			return fmt.Errorf("configure project i18n extension: %w", err)
		}
	}
	var cfg config.Config
	var configSnapshot config.Snapshot
	if options.ProjectConfigFile != "" {
		if dependencies.loadProjectConfig == nil {
			return fmt.Errorf("load Runtime project configuration: dependency is unavailable")
		}
		cfg, configSnapshot, err = dependencies.loadProjectConfig(options.ProjectConfigFile)
	} else {
		cfg, configSnapshot, err = dependencies.loadConfig()
	}
	if err != nil {
		return fmt.Errorf("load Runtime configuration: %w", err)
	}
	cfg.RuntimeVersion = options.Identity.RuntimeVersion
	identityFactory := options.IdentityFactory
	if identityFactory == nil {
		return fmt.Errorf("configure Identity factory: generated project composition did not supply an SDK Factory")
	}
	notificationFactory := options.NotificationFactory
	if notificationFactory == nil {
		return fmt.Errorf("configure Notification factory: generated project composition did not supply an SDK Factory")
	}
	for _, entry := range configSnapshot.StartupReport() {
		zap.L().Info("Runtime configuration", zap.String("name", entry.Name), zap.String("source", entry.Source), zap.String("version", entry.Version), zap.Bool("redacted", entry.Redacted))
	}
	for _, warning := range configSnapshot.Warnings {
		zap.L().Warn("Runtime configuration warning", zap.String("warning", warning))
	}
	zap.L().Info("Runtime configuration snapshot", zap.String("revision", configSnapshot.Revision))
	lifecycleCtx, stop := dependencies.notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, telemetryErr := dependencies.initializeTracer(lifecycleCtx, telemetry.Config{
		ServiceName: "domainry-domain-runtime", ServiceVersion: cfg.RuntimeVersion,
		Exporter: cfg.TelemetryExporter, Endpoint: cfg.TelemetryEndpoint, Headers: cfg.TelemetryHeaders,
		Insecure: cfg.TelemetryInsecure, SampleRatio: cfg.TelemetrySampleRatio, ExportTimeout: cfg.TelemetryExportTimeout,
	})
	if telemetryErr != nil {
		zap.L().Warn("telemetry exporter disabled after initialization failure", zap.String("error_kind", "telemetry_initialization_failed"))
		shutdownTelemetry = func(context.Context) error { return nil }
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(lifecycleCtx), cfg.TelemetryExportTimeout)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			zap.L().Warn("telemetry shutdown failed", zap.String("error_kind", "telemetry_shutdown_failed"))
		}
	}()
	projectDatabaseConfig := cfg
	projectDatabaseConfig.DatabaseMigrationDSN = ""
	projectDatabase, err := dependencies.prepareDatabase(context.WithoutCancel(lifecycleCtx), projectDatabaseConfig)
	if err != nil {
		return fmt.Errorf("prepare project database: %w", err)
	}
	defer func() {
		_ = projectDatabase.CloseContext(context.WithoutCancel(lifecycleCtx))
	}()
	businessProfileProjection := newRuntimeBusinessProfileProjection(projectDatabase)
	identityBinding, identityHTTPSurfaces, err := openProjectIdentity(lifecycleCtx, cfg, identityFactory, projectIdentityDatabaseHandle(projectDatabase, cfg.DBPath, businessProfileProjection))
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(lifecycleCtx), cfg.HTTPShutdownTimeout)
		defer cancel()
		if err := identityBinding.Close(closeCtx); err != nil {
			zap.L().Error("close Identity integration", zap.Error(err))
		}
	}()

	listenerDefinitions := runtimeHTTPListeners(cfg)
	handlers := make(map[runtimehttp.SurfaceRouteGroup]*bootstrap.EntrypointMux, len(listenerDefinitions))
	for _, listener := range listenerDefinitions {
		handlers[listener.group] = &bootstrap.EntrypointMux{}
	}
	lifecycleState, lifecycleFound, lifecycleErr := provision.ReadLifecycle(cfg.ManifestPath)
	if lifecycleErr != nil {
		return fmt.Errorf("load Runtime lifecycle: %w", lifecycleErr)
	}
	if lifecycleFound && (lifecycleState.Status == provision.LifecycleStatusConfiguring || lifecycleState.Status == provision.LifecycleStatusValidating || lifecycleState.Status == provision.LifecycleStatusVerifying) {
		cfg.AllowEmptyAuthoringManifest = true
		for _, handler := range handlers {
			handler.SetConfiguring(lifecycleState.BuilderTaskID)
		}
	}
	closeRuntime := func(runtime runtimeProcess) error {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(lifecycleCtx), cfg.HTTPShutdownTimeout)
		defer cancel()
		return runtime.CloseContext(closeCtx)
	}
	activator := &runtimeActivator{
		handlers: handlers, connectorGateway: connectorGateway,
		close: closeRuntime,
		start: func(manifest manifestmodel.ManifestSchema) (runtimeProcess, error) {
			if err := validateDomainSDKTarget(options.Identity.DomainSDK, manifest.GeneratedDomainSDK); err != nil {
				return nil, err
			}
			runtimeConfig := cfg
			if manifest.SourceBlueprintID == provision.DirectAuthoringSourceID && len(manifest.Objects) == 0 {
				runtimeConfig.AllowEmptyAuthoringManifest = true
			}
			runtime := dependencies.newRuntime(lifecycleCtx, runtimeConfig, businessHandlers, connectorProviders, releaseIdentity, artifactEvidence, identityBinding, notificationFactory, projectDatabase)
			if runtime == nil {
				return nil, errors.New("Runtime bootstrap returned no process")
			}
			bound, started := false, false
			defer func() {
				if started {
					return
				}
				if bound {
					connectorGateway.unbind()
				}
				_ = closeRuntime(runtime)
			}()
			if bindErr := connectorGateway.bind(runtime.connectorGateway()); bindErr != nil {
				return nil, bindErr
			}
			bound = true
			runtime.StartWorkers(lifecycleCtx)
			businessProfileProjection.Publish(manifest.Objects, manifest.IdentityProfileExtensions)
			started = true
			return runtime, nil
		},
	}
	provisionServer := provision.NewServerWithConfiguringLifecycle(cfg.ManifestPath, cfg.RuntimeVersion, provision.ContractIdentity{
		ServiceKind: runtimehttp.BusinessRuntimeServiceKind, APIContractVersion: runtimehttp.BusinessRuntimeAPIContractVersion, APIContractHash: runtimehttp.BusinessRuntimeAPIContractHash(),
	}, activator.Activate, func(state provision.LifecycleState) {
		for _, handler := range handlers {
			handler.SetConfiguring(state.BuilderTaskID)
		}
	}).UseProductBrand(cfg.EffectiveProductBrandName()).UseAbandonRuntime(activator.Abandon)
	provisionRoutes := provisionServer.Routes()
	for group, handler := range handlers {
		if group == runtimehttp.SurfaceRouteGroupPublic || group == runtimehttp.SurfaceRouteGroupAll {
			handler.SetProvision(provisionRoutes)
		}
	}
	if _, err := dependencies.stat(cfg.ManifestPath); err == nil {
		rawManifest, readErr := dependencies.readFile(cfg.ManifestPath)
		if readErr != nil {
			return fmt.Errorf("read Runtime manifest: %w", readErr)
		}
		manifest, _, decodeErr := manifestmodel.DecodeManifest(rawManifest)
		if decodeErr != nil {
			return fmt.Errorf("decode Runtime manifest: %w", decodeErr)
		}
		if err := activator.Activate(manifest); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		zap.L().Info("starting domain Runtime in unauthenticated Provision mode", zap.String("manifest_path", cfg.ManifestPath))
	} else {
		return err
	}
	defer activator.Close()

	servers := make([]*http.Server, 0, len(listenerDefinitions))
	for _, listener := range listenerDefinitions {
		listenerHandler := http.Handler(handlers[listener.group])
		if listener.group == runtimehttp.SurfaceRouteGroupPublic || listener.group == runtimehttp.SurfaceRouteGroupAll {
			listenerHandler = frontendAssets.wrap(listenerHandler)
		}
		listenerHandler, err = mountIdentityHTTPSurfaces(listener.group, identityHTTPSurfaces, listenerHandler)
		if err != nil {
			return fmt.Errorf("mount Identity HTTP surfaces on %s listener: %w", listener.name, err)
		}
		endpointCount := runtimehttp.SurfaceRouteGroupEndpointCount(listener.group)
		zap.L().Info("starting domain Runtime listener",
			zap.String("listener", listener.name),
			zap.String("address", listener.addr),
			zap.String("surface_group", string(listener.group)),
			zap.Int("endpoint_count", endpointCount),
		)
		servers = append(servers, &http.Server{
			Addr:              listener.addr,
			Handler:           listenerHandler,
			ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
			ReadTimeout:       cfg.HTTPReadTimeout,
			WriteTimeout:      cfg.HTTPWriteTimeout,
			IdleTimeout:       cfg.HTTPIdleTimeout,
			MaxHeaderBytes:    cfg.HTTPMaxHeaderBytes,
		})
	}
	serveErr := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) { serveErr <- dependencies.listenAndServe(server) }(server)
	}
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-lifecycleCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(lifecycleCtx), cfg.HTTPShutdownTimeout)
		defer cancel()
		var shutdownErrors []error
		for _, server := range servers {
			if err := dependencies.shutdown(shutdownCtx, server); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
		if err := errors.Join(shutdownErrors...); err != nil {
			return err
		}
		return nil
	}
}

func validateDomainSDKTarget(compiled DomainSDKIdentity, target *manifestmodel.GeneratedDomainSDKIdentity) error {
	if compiled.isZero() {
		if target == nil {
			return nil
		}
		return ErrDomainSDKContractRequired
	}
	if target == nil {
		return ErrDomainSDKTargetRequired
	}
	fields := []struct {
		name     string
		compiled string
		target   string
	}{
		{"contract_version", compiled.ContractVersion, target.ContractVersion},
		{"contract_sha256", compiled.ContractSHA256, target.ContractSHA256},
		{"generator_version", compiled.GeneratorVersion, target.GeneratorVersion},
		{"metadata_snapshot_sha256", compiled.MetadataSnapshotSHA256, target.MetadataSnapshotSHA256},
		{"runtimeext_contract_sha256", compiled.RuntimeextContractSHA256, target.RuntimeextContractSHA256},
		{"build_constraint", compiled.BuildConstraint, target.BuildConstraint},
		{"artifact_sha256", compiled.ArtifactSHA256, target.ArtifactSHA256},
	}
	for _, field := range fields {
		if field.compiled != field.target {
			return fmt.Errorf("%w: field=%s compiled=%q target=%q", ErrDomainSDKTargetMismatch, field.name, field.compiled, field.target)
		}
	}
	return nil
}
