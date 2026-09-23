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

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/telemetry"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/coderuntime"
	"github.com/domainry/domainry-runtime/pkg/codingruntime"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	projectvalidation "github.com/domainry/domainry-runtime/runtime/domain/project/validation"
	recordtimerprojection "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/projection"
	principalcache "github.com/domainry/domainry-runtime/runtime/infrastructure/principalcache"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"go.uber.org/zap"
)

// Run validates project-owned composition, freezes its Handler registry
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

type runtimeListenerProcess interface {
	RoutesForListenerGroup(runtimehttp.ListenerRouteGroup) http.Handler
}

type runtimeModuleAdapterProcess interface {
	ModuleHTTPAdapters() []modulehttp.Adapter
}

type bootstrapRuntimeProcess struct{ *bootstrap.Runtime }

func (r bootstrapRuntimeProcess) StartWorkers(ctx context.Context) {
	bootstrap.StartWorkers(ctx, r.Runtime)
}

func (r bootstrapRuntimeProcess) RoutesForListenerGroup(group runtimehttp.ListenerRouteGroup) http.Handler {
	return bootstrap.RoutesForListenerGroup(r.Runtime, group)
}

func (r bootstrapRuntimeProcess) ModuleHTTPAdapters() []modulehttp.Adapter {
	return r.Runtime.ModuleHTTPAdapters()
}

func (r bootstrapRuntimeProcess) connectorGateway() runtimeConnectorGateway {
	if r.Runtime == nil {
		return unavailableRuntimeConnectorGateway{}
	}
	return integrationRuntimeConnectorGateway{operations: r.Runtime.IntegrationOwnerOperations()}
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
	prepareDatabase      func(context.Context, config.Config, bootstrap.RuntimeSchemaCapabilities) (*bootstrap.ProjectDatabase, error)
	newRuntime           func(context.Context, config.Config, *runtimeext.ProjectExtensionRegistry, *connector.Registry, runtimehttp.RuntimeReleaseIdentity, bootstrap.RuntimeReleaseArtifactEvidence, identitysdk.Binding, notificationsdk.Factory, monitoringsdk.Factory, schedulersdk.Factory, dataexchangesdk.Factory, agentsdk.Factory, integrationsdk.Factory, reportsdk.Factory, *bootstrap.ProjectDatabase, bootstrap.ProjectStartupOptions) runtimeProcess
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
		newRuntime: func(ctx context.Context, cfg config.Config, handlers *runtimeext.ProjectExtensionRegistry, connectors *connector.Registry, identity runtimehttp.RuntimeReleaseIdentity, evidence bootstrap.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, monitoringFactory monitoringsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, agentFactory agentsdk.Factory, integrationFactory integrationsdk.Factory, reportFactory reportsdk.Factory, database *bootstrap.ProjectDatabase, startupOptions bootstrap.ProjectStartupOptions) runtimeProcess {
			return bootstrapRuntimeProcess{Runtime: bootstrap.NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(ctx, cfg, handlers, connectors, identity, evidence, binding, notificationFactory, monitoringFactory, schedulerFactory, dataExchangeFactory, integrationFactory, reportFactory, database, startupOptions, agentFactory)}
		},
		listenAndServe: func(server *http.Server) error { return server.ListenAndServe() },
		shutdown:       func(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) },
	}
}

type runtimeActivator struct {
	handler          *bootstrap.EntrypointMux
	handlers         map[runtimehttp.ListenerRouteGroup]*bootstrap.EntrypointMux
	connectorGateway *bindableConnectorGateway
	start            func() (runtimeProcess, error)
	close            func(runtimeProcess) error
	mu               sync.Mutex
	runtime          runtimeProcess
}

func (a *runtimeActivator) entrypointHandlers() map[runtimehttp.ListenerRouteGroup]*bootstrap.EntrypointMux {
	if len(a.handlers) != 0 {
		return a.handlers
	}
	if a.handler != nil {
		return map[runtimehttp.ListenerRouteGroup]*bootstrap.EntrypointMux{
			runtimehttp.ListenerRouteGroupAll: a.handler,
		}
	}
	return nil
}

func (a *runtimeActivator) Activate() (err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime != nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			// Bootstrap reports startup failures by panicking with the error;
			// wrapping keeps typed outcomes such as the plan-mode sentinel visible.
			if recoveredErr, ok := recovered.(error); ok {
				err = fmt.Errorf("Runtime bootstrap failed during project activation: %w", recoveredErr)
				return
			}
			err = fmt.Errorf("Runtime bootstrap failed during project activation: %v", recovered)
		}
	}()
	runtime, startErr := a.start()
	if startErr != nil {
		return startErr
	}
	a.runtime = runtime
	if listenerRuntime, ok := runtime.(runtimeListenerProcess); ok {
		for group, handler := range a.entrypointHandlers() {
			handler.SetBusiness(listenerRuntime.RoutesForListenerGroup(group))
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

type runtimeHTTPListener struct {
	name  string
	addr  string
	group runtimehttp.ListenerRouteGroup
}

func runtimeHTTPListeners(cfg config.Config) []runtimeHTTPListener {
	listeners := []runtimeHTTPListener{}
	if strings.TrimSpace(cfg.HTTPPublicAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "public", addr: strings.TrimSpace(cfg.HTTPPublicAddr), group: runtimehttp.ListenerRouteGroupPublic})
	}
	if strings.TrimSpace(cfg.HTTPManagementAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "management", addr: strings.TrimSpace(cfg.HTTPManagementAddr), group: runtimehttp.ListenerRouteGroupManagement})
	}
	if strings.TrimSpace(cfg.HTTPOpsAddr) != "" {
		listeners = append(listeners, runtimeHTTPListener{name: "ops", addr: strings.TrimSpace(cfg.HTTPOpsAddr), group: runtimehttp.ListenerRouteGroupOps})
	}
	if len(listeners) == 0 {
		listeners = append(listeners, runtimeHTTPListener{name: "legacy-all", addr: cfg.HTTPAddr(), group: runtimehttp.ListenerRouteGroupAll})
	}
	return listeners
}

func (a *runtimeActivator) closeRuntime(runtime runtimeProcess) error {
	if a.close != nil {
		return a.close(runtime)
	}
	return fmt.Errorf("Runtime activator close requires its lifecycle callback")
}

func prepareProjectExtensions(options Options) (*runtimeext.ProjectExtensionRegistry, *bindableConnectorGateway, error) {
	if err := options.Identity.Validate(); err != nil {
		return nil, nil, err
	}
	gateway := &bindableConnectorGateway{}
	extensions := runtimeext.ProjectExtensions{}
	if options.ProjectExtensions != nil {
		var err error
		extensions, err = options.ProjectExtensions(gateway)
		if err != nil {
			return nil, nil, fmt.Errorf("build project extensions: %w", err)
		}
	}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterProjectExtensions(extensions); err != nil {
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
	projectExtensions, connectorGateway, err := prepareProjectExtensions(options)
	if err != nil {
		return fmt.Errorf("validate project Runtime composition: %w", err)
	}
	connectorProviders, err := prepareConnectorProviders(options)
	if err != nil {
		return fmt.Errorf("validate project Runtime composition: %w", err)
	}
	releaseIdentity, err := runtimeReleaseIdentity(options.Identity, projectExtensions, connectorProviders)
	if err != nil {
		return fmt.Errorf("validate project Runtime release identity: %w", err)
	}
	artifactEvidence := bootstrap.RuntimeReleaseArtifactEvidence{Verified: true}
	executablePath := ""
	if dependencies.executable != nil {
		if resolvedExecutablePath, executableErr := dependencies.executable(); executableErr == nil {
			executablePath = resolvedExecutablePath
		}
	}
	frontendAssets, err := loadProjectFrontendAssets(executablePath, "", dependencies.readFile)
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
	modelFile := strings.TrimSpace(options.ModelFile)
	if modelFile == "" {
		return fmt.Errorf("load project model: ModelFile is required")
	}
	rawModel, err := dependencies.readFile(modelFile)
	if err != nil {
		return fmt.Errorf("read project model %s: %w", modelFile, err)
	}
	projectModel, err := projectmodel.Decode(rawModel)
	if err != nil {
		return fmt.Errorf("decode project model %s: %w", modelFile, err)
	}
	if err := projectvalidation.ValidateComposition(projectModel, projectExtensions, nil); err != nil {
		return fmt.Errorf("validate project model and code definitions: %w", err)
	}
	runtimeModel, err := projectmodel.Compile(projectModel)
	if err != nil {
		return fmt.Errorf("compile project storage and authorization model: %w", err)
	}
	if err := attachPublicResources(&runtimeModel, projectExtensions.ProjectDefinitions().PublicResources); err != nil {
		return fmt.Errorf("attach code-owned public resources: %w", err)
	}
	// Runtime-owned record timer storage is code-owned infrastructure. It is
	// always materialized with the project model, but never authored in
	// model.json and never participates in the project content hash.
	runtimeModel.Objects = append(runtimeModel.Objects, recordtimerprojection.RecordTimerSystemObjects()...)
	runtimeSchemaCapabilities := bootstrap.ProjectSchemaCapabilities(runtimeModel, projectExtensions)
	runtimeSchemaCapabilities.ReleaseCoordination = cfg.RuntimeReplicaCount > 1
	cfg.RuntimeVersion = options.Identity.RuntimeVersion
	identityFactory := options.IdentityFactory
	if identityFactory == nil {
		return fmt.Errorf("configure Identity factory: generated project composition did not supply an SDK Factory")
	}
	notificationFactory := options.NotificationFactory
	monitoringFactory := options.MonitoringFactory
	schedulerFactory := options.SchedulerFactory
	dataExchangeFactory := options.DataExchangeFactory
	agentFactory := options.AgentFactory
	integrationFactory := options.IntegrationFactory
	reportFactory := options.ReportFactory
	for _, entry := range configSnapshot.StartupReport() {
		zap.L().Info("Runtime configuration", zap.String("name", entry.Name), zap.String("source", entry.Source), zap.String("version", entry.Version), zap.Bool("redacted", entry.Redacted))
	}
	for _, warning := range configSnapshot.Warnings {
		zap.L().Warn("Runtime configuration warning", zap.String("warning", warning))
	}
	zap.L().Info("Runtime configuration snapshot", zap.String("revision", configSnapshot.Revision))
	lifecycleCtx, stop := dependencies.notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	modulePrincipalCache, err := principalcache.Open(context.WithoutCancel(lifecycleCtx), cfg)
	if err != nil {
		return fmt.Errorf("open Identity principal cache: %w", err)
	}
	defer func() {
		if closer, ok := modulePrincipalCache.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				zap.L().Warn("close Identity principal cache", zap.String("error_kind", "identity_principal_cache_close_failed"), zap.Error(err))
			}
		}
	}()
	principalResolverOptions := identityprincipal.Options{
		MaxCacheTTL: cfg.EffectivePrincipalCacheTTL(),
		Cache:       modulePrincipalCache,
		OnCacheError: func(err error) {
			zap.L().Warn("Identity principal cache operation failed; resolving from authoritative binding", zap.String("error_kind", "identity_principal_cache_operation_failed"), zap.Error(err))
		},
	}
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
	projectDatabase, err := dependencies.prepareDatabase(context.WithoutCancel(lifecycleCtx), projectDatabaseConfig, runtimeSchemaCapabilities)
	if err != nil {
		return fmt.Errorf("prepare project database: %w", err)
	}
	defer func() {
		_ = projectDatabase.CloseContext(context.WithoutCancel(lifecycleCtx))
	}()
	businessProfileProjection := newRuntimeBusinessProfileProjection(projectDatabase)
	workspaceManager, err := newProjectWorkspaceManager(context.WithoutCancel(lifecycleCtx), cfg, identityFactory, projectDatabase, projectIdentityDatabaseHandle(projectDatabase, cfg.DBPath, businessProfileProjection, projectIdentityUsageOptions{
		ApplicationKey: cfg.IdentityAudience, CursorSecret: cfg.AuditExportTokenKey,
	}), options.InitialWorkspaceCredentialDelivery, options.InstallationAdministratorCredentialDelivery)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(lifecycleCtx), cfg.HTTPShutdownTimeout)
		defer cancel()
		if err := workspaceManager.Close(closeCtx); err != nil {
			zap.L().Error("close Identity integration", zap.Error(err))
		}
	}()

	listenerDefinitions := runtimeHTTPListeners(cfg)
	handlers := make(map[runtimehttp.ListenerRouteGroup]*bootstrap.EntrypointMux, len(listenerDefinitions))
	for _, listener := range listenerDefinitions {
		handlers[listener.group] = &bootstrap.EntrypointMux{}
	}
	identityRouters := make(map[runtimehttp.ListenerRouteGroup]*identityAdapterRouter, len(handlers))
	var initialModuleGuard moduleRouteGuard
	if binding := workspaceManager.Binding(); binding != nil {
		initialModuleGuard, err = newModuleHTTPRouteGuard(binding, principalResolverOptions)
		if err != nil {
			return err
		}
	}
	for group, handler := range handlers {
		identityRouters[group] = newIdentityAdapterRouter(group, handler)
		identityRouters[group].corsOrigins = cfg.CORSAllowedOrigins
		if err := identityRouters[group].Bind(workspaceManager.Adapters(), initialModuleGuard); err != nil {
			return fmt.Errorf("mount initialized Identity HTTP adapters: %w", err)
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
		start: func() (runtimeProcess, error) {
			if err := workspaceManager.Activate(context.WithoutCancel(lifecycleCtx), runtimeModel, projectExtensions.WorkspaceBootstrapParticipant(), projectExtensions.BusinessHandlerDescriptors()...); err != nil {
				return nil, err
			}
			identityBinding := workspaceManager.Binding()
			if identityBinding == nil {
				return nil, errors.New("initialized Workspace returned no Identity binding")
			}
			moduleGuard, err := newModuleHTTPRouteGuard(identityBinding, principalResolverOptions)
			if err != nil {
				return nil, err
			}
			for group, router := range identityRouters {
				if err := router.Bind(workspaceManager.Adapters(), moduleGuard); err != nil {
					return nil, fmt.Errorf("mount Identity HTTP adapters on %s listener: %w", group, err)
				}
			}
			runtimeConfig := workspaceManager.Config()
			var analysisTableSource reportmodulehost.AnalysisTableSource
			if options.AnalysisTableSourceFactory != nil {
				analysisTableSource, err = options.AnalysisTableSourceFactory(runtimeConfig.RuntimeInstanceID)
				if err != nil {
					return nil, fmt.Errorf("open project analysis table source: %w", err)
				}
			}
			var codeRuntime agentsdk.ConversationCodeRuntime
			if strings.TrimSpace(executablePath) != "" {
				codeRuntime, err = coderuntime.NewProcess(executablePath)
				if err != nil {
					return nil, fmt.Errorf("prepare Agent code Runtime: %w", err)
				}
			}
			var codingRuntime agentsdk.ConversationCodingRuntime
			if options.AgentCodingWorkspace != nil {
				configured := *options.AgentCodingWorkspace
				codingRuntime, err = codingruntime.New(configured)
				if err != nil {
					return nil, fmt.Errorf("prepare Agent coding Runtime: %w", err)
				}
			}
			developmentData := bootstrap.DevelopmentDataOptions{}
			if options.DevelopmentData != nil {
				developmentData = bootstrap.DevelopmentDataOptions{
					Enabled: true, Seed: options.DevelopmentData.Seed,
					RecordsPerObject: options.DevelopmentData.RecordsPerObject,
				}
			}
			runtime := dependencies.newRuntime(lifecycleCtx, runtimeConfig, projectExtensions, connectorProviders, releaseIdentity, artifactEvidence, identityBinding, notificationFactory, monitoringFactory, schedulerFactory, dataExchangeFactory, agentFactory, integrationFactory, reportFactory, projectDatabase, bootstrap.ProjectStartupOptions{
				ConversationCodeRuntime:   codeRuntime,
				ConversationCodingRuntime: codingRuntime,
				DevelopmentData:           developmentData,
				ProjectModel:              runtimeModel,
				AnalysisTableSource:       analysisTableSource,
				BlobStore:                 options.BlobStore,
				FileScanner:               options.FileScanner,
				ProjectHTTP:               options.ProjectHTTP,
			})
			if runtime == nil {
				return nil, errors.New("Runtime bootstrap returned no process")
			}
			moduleAdapters := append([]modulehttp.Adapter(nil), workspaceManager.Adapters()...)
			if provider, ok := runtime.(runtimeModuleAdapterProcess); ok {
				moduleAdapters = append(moduleAdapters, provider.ModuleHTTPAdapters()...)
			}
			for group, router := range identityRouters {
				if err := router.Bind(moduleAdapters); err != nil {
					return nil, fmt.Errorf("mount module HTTP adapters on %s listener: %w", group, err)
				}
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
			businessProfileProjection.Publish(runtimeModel.Objects, runtimeModel.IdentityProfiles)
			started = true
			return runtime, nil
		},
	}
	if err := activator.Activate(); err != nil {
		return err
	}
	defer activator.Close()

	servers := make([]*http.Server, 0, len(listenerDefinitions))
	for _, listener := range listenerDefinitions {
		listenerHandler := http.Handler(identityRouters[listener.group])
		if listener.group == runtimehttp.ListenerRouteGroupPublic || listener.group == runtimehttp.ListenerRouteGroupAll {
			listenerHandler = frontendAssets.wrap(listenerHandler, options.ProjectHTTP != nil)
		}
		endpointCount := runtimehttp.ListenerRouteGroupEndpointCount(listener.group)
		zap.L().Info("starting domain Runtime listener",
			zap.String("listener", listener.name),
			zap.String("address", listener.addr),
			zap.String("listener_group", string(listener.group)),
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
