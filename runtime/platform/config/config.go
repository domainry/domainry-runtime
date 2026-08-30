package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

const DevIntegrationSecret = "dev-generated-integration-secret-change-me"
const DevAuditExportTokenKey = "dev-generated-audit-export-token-change-me"

type Config struct {
	RuntimeVersion                          string
	RuntimeInstanceID                       string
	ProductBrandName                        string
	Environment                             string
	AppLocale                               string
	HTTPBindHost                            string
	Port                                    string
	HTTPPublicAddr                          string
	HTTPTenantAdminAddr                     string
	HTTPOpsAddr                             string
	HTTPOpsAllowPublicBindBreakGlass        bool
	HTTPOpsPublicBindBreakGlassReason       string
	HTTPReadHeaderTimeout                   time.Duration
	HTTPReadTimeout                         time.Duration
	HTTPWriteTimeout                        time.Duration
	HTTPIdleTimeout                         time.Duration
	HTTPShutdownTimeout                     time.Duration
	HTTPMaxJSONBodyBytes                    int
	HTTPMaxHeaderBytes                      int
	HTTPPublicMaxJSONBodyBytes              int
	HTTPTenantAdminMaxJSONBodyBytes         int
	HTTPOpsMaxJSONBodyBytes                 int
	HTTPPublicRequestTimeout                time.Duration
	HTTPTenantAdminRequestTimeout           time.Duration
	HTTPOpsRequestTimeout                   time.Duration
	HTTPPublicRateLimitPerMinute            int
	HTTPTenantAdminRateLimitPerMinute       int
	HTTPOpsRateLimitPerMinute               int
	CapacityGlobalInFlight                  int
	CapacityWorkspaceInFlight               int
	CapacityUseCaseInFlight                 int
	CapacityRetryInFlight                   int
	CapacityGlobalRatePerMinute             int
	CapacityWorkspaceRatePerMinute          int
	CapacityUseCaseRatePerMinute            int
	CapacityMaxWorkspaceStates              int
	CapacityMaxUseCaseStates                int
	CapacityWorkspaceStateTTL               time.Duration
	CapacityRequestTimeout                  time.Duration
	CapacityDegradedRatio                   float64
	CapacityRecoveryRatio                   float64
	CapacityRetryAfter                      time.Duration
	CapacityConnectorGlobalInFlight         int
	CapacityConnectorWorkspaceInFlight      int
	CapacityConnectorProviderInFlight       int
	CapacityConnectorGlobalRatePerMinute    int
	CapacityConnectorWorkspaceRatePerMinute int
	CapacityConnectorProviderRatePerMinute  int
	CapacityQueueDepthThreshold             int
	CapacityQueueOldestAgeThreshold         time.Duration
	BusinessEventReplayLimit                int
	BusinessEventSubscriberBuffer           int
	BusinessEventGlobalConnections          int
	BusinessEventWorkspaceConnections       int
	BusinessEventPrincipalConnections       int
	BusinessEventHeartbeatInterval          time.Duration
	BusinessEventRetryInterval              time.Duration
	TelemetryExporter                       string
	TelemetryEndpoint                       string
	TelemetryHeaders                        map[string]string
	TelemetryInsecure                       bool
	TelemetrySampleRatio                    float64
	TelemetryExportTimeout                  time.Duration
	HealthCheckTimeout                      time.Duration
	DatabaseDriver                          string
	DatabaseDSN                             string
	DatabaseMigrationDSN                    string
	DatabaseMigrationMode                   string
	DatabaseMinSchemaVersion                string
	DatabaseMaxSchemaVersion                string
	DatabaseConnectionMode                  string
	DatabaseSchema                          string
	DatabaseMaxOpenConns                    int
	DatabaseMaxIdleConns                    int
	DatabaseMaxConnections                  int
	DatabaseReservedConnections             int
	RuntimeReplicaCount                     int
	DatabaseConnMaxLifetime                 time.Duration
	DatabaseConnMaxIdleTime                 time.Duration
	DatabaseConnectTimeout                  time.Duration
	DatabaseStatementTimeout                time.Duration
	DatabaseLockTimeout                     time.Duration
	DatabaseSSLRootCert                     string
	DatabaseRLSEnabled                      bool
	DBPath                                  string
	ManifestPath                            string
	FrontendCapabilityManifestPath          string
	MigrationDir                            string
	MigrationSQL                            string
	MigrationBackupDir                      string
	MigrationBackupEvidencePath             string
	MigrationBackupLastSuccessAt            string
	MigrationRestoreDrillSuccessAt          string
	MigrationOperator                       string
	MigrationInstanceID                     string
	SkipManifestValidation                  bool
	BusinessSeedSyncDisabled                bool
	// AllowEmptyAuthoringManifest is set only by the trusted configuring
	// Provision lifecycle. It is not loaded from environment configuration.
	AllowEmptyAuthoringManifest    bool
	UploadDir                      string
	CORSAllowedOrigins             []string
	SurfaceBusinessOrigins         []string
	SurfaceAdminOrigins            []string
	SurfacePortalOrigins           []string
	RuntimeAllowDevIdentityHeaders bool
	AuditExportTokenKey            string
	IdentityRedirectURLs           []string
	IdentityWorkspaceID            string
	IdentityAudience               string
	NotificationTenantID           string
	NotificationWorkspaceID        string
	NotificationApplicationKey     string
	PartyTenantID                  string
	PartyWorkspaceID               string
	PartyApplicationKey            string
	IntegrationSecretKey           string
	IntegrationActiveKeyID         string
	IntegrationDecryptOnlyKeys     map[string]string
	AgentHTTPBaseURL               string
	AgentHTTPAPIKey                string
	AgentHTTPAgentID               int
	AgentHTTPTimeout               time.Duration
	AgentHTTPRateLimitPerMinute    int
	WorkerPollInterval             time.Duration
	WorkerBatchSize                int
	SchedulerEnabled               bool
	SchedulerPollInterval          time.Duration
	SchedulerBatchSize             int
	SchedulerLeaseTTL              time.Duration
	SchedulerMaxCatchupWindows     int
}

func FromEnv() Config {
	environment := env("APP_ENV", env("GO_ENV", env("NODE_ENV", "development")))
	schedulerPollDefault := 500 * time.Millisecond
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		schedulerPollDefault = 30 * time.Second
	}
	return Config{
		RuntimeVersion:                          env("DOMAINRY_RUNTIME_VERSION", "dev"),
		RuntimeInstanceID:                       strings.TrimSpace(os.Getenv("RUNTIME_INSTANCE_ID")),
		ProductBrandName:                        productbrand.NameFromEnvironment(),
		Environment:                             environment,
		AppLocale:                               env("APP_LOCALE", "en-US"),
		HTTPBindHost:                            strings.TrimSpace(os.Getenv("HTTP_BIND_HOST")),
		Port:                                    env("PORT", "8081"),
		HTTPPublicAddr:                          strings.TrimSpace(os.Getenv("HTTP_PUBLIC_ADDR")),
		HTTPTenantAdminAddr:                     strings.TrimSpace(os.Getenv("HTTP_TENANT_ADMIN_ADDR")),
		HTTPOpsAddr:                             strings.TrimSpace(os.Getenv("HTTP_OPS_ADDR")),
		HTTPOpsAllowPublicBindBreakGlass:        boolEnv("HTTP_OPS_ALLOW_PUBLIC_BIND_BREAK_GLASS", false),
		HTTPOpsPublicBindBreakGlassReason:       strings.TrimSpace(os.Getenv("HTTP_OPS_PUBLIC_BIND_BREAK_GLASS_REASON")),
		HTTPReadHeaderTimeout:                   durationEnv("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		HTTPReadTimeout:                         durationEnv("HTTP_READ_TIMEOUT", 30*time.Second),
		HTTPWriteTimeout:                        durationEnv("HTTP_WRITE_TIMEOUT", 2*time.Minute),
		HTTPIdleTimeout:                         durationEnv("HTTP_IDLE_TIMEOUT", time.Minute),
		HTTPShutdownTimeout:                     durationEnv("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
		HTTPMaxJSONBodyBytes:                    intEnv("HTTP_MAX_JSON_BODY_BYTES", 2<<20),
		HTTPMaxHeaderBytes:                      intEnv("HTTP_MAX_HEADER_BYTES", 1<<20),
		HTTPPublicMaxJSONBodyBytes:              intEnv("HTTP_PUBLIC_MAX_JSON_BODY_BYTES", 2<<20),
		HTTPTenantAdminMaxJSONBodyBytes:         intEnv("HTTP_TENANT_ADMIN_MAX_JSON_BODY_BYTES", 2<<20),
		HTTPOpsMaxJSONBodyBytes:                 intEnv("HTTP_OPS_MAX_JSON_BODY_BYTES", 1<<20),
		HTTPPublicRequestTimeout:                durationEnv("HTTP_PUBLIC_REQUEST_TIMEOUT", 30*time.Second),
		HTTPTenantAdminRequestTimeout:           durationEnv("HTTP_TENANT_ADMIN_REQUEST_TIMEOUT", 30*time.Second),
		HTTPOpsRequestTimeout:                   durationEnv("HTTP_OPS_REQUEST_TIMEOUT", 15*time.Second),
		HTTPPublicRateLimitPerMinute:            intEnv("HTTP_PUBLIC_RATE_LIMIT_PER_MINUTE", 6000),
		HTTPTenantAdminRateLimitPerMinute:       intEnv("HTTP_TENANT_ADMIN_RATE_LIMIT_PER_MINUTE", 3000),
		HTTPOpsRateLimitPerMinute:               intEnv("HTTP_OPS_RATE_LIMIT_PER_MINUTE", 1200),
		CapacityGlobalInFlight:                  intEnv("CAPACITY_GLOBAL_IN_FLIGHT", 256),
		CapacityWorkspaceInFlight:               intEnv("CAPACITY_WORKSPACE_IN_FLIGHT", 32),
		CapacityUseCaseInFlight:                 intEnv("CAPACITY_USE_CASE_IN_FLIGHT", 64),
		CapacityRetryInFlight:                   intEnv("CAPACITY_RETRY_IN_FLIGHT", 16),
		CapacityGlobalRatePerMinute:             intEnv("CAPACITY_GLOBAL_RATE_PER_MINUTE", 6000),
		CapacityWorkspaceRatePerMinute:          intEnv("CAPACITY_WORKSPACE_RATE_PER_MINUTE", 600),
		CapacityUseCaseRatePerMinute:            intEnv("CAPACITY_USE_CASE_RATE_PER_MINUTE", 1200),
		CapacityMaxWorkspaceStates:              intEnv("CAPACITY_MAX_WORKSPACE_STATES", 10_000),
		CapacityMaxUseCaseStates:                intEnv("CAPACITY_MAX_USE_CASE_STATES", 1024),
		CapacityWorkspaceStateTTL:               durationEnv("CAPACITY_WORKSPACE_STATE_TTL", 30*time.Minute),
		CapacityRequestTimeout:                  durationEnv("CAPACITY_REQUEST_TIMEOUT", 30*time.Second),
		CapacityDegradedRatio:                   floatEnv("CAPACITY_DEGRADED_RATIO", .8),
		CapacityRecoveryRatio:                   floatEnv("CAPACITY_RECOVERY_RATIO", .6),
		CapacityRetryAfter:                      durationEnv("CAPACITY_RETRY_AFTER", 2*time.Second),
		CapacityConnectorGlobalInFlight:         intEnv("CAPACITY_CONNECTOR_GLOBAL_IN_FLIGHT", 64),
		CapacityConnectorWorkspaceInFlight:      intEnv("CAPACITY_CONNECTOR_WORKSPACE_IN_FLIGHT", 16),
		CapacityConnectorProviderInFlight:       intEnv("CAPACITY_CONNECTOR_PROVIDER_IN_FLIGHT", 8),
		CapacityConnectorGlobalRatePerMinute:    intEnv("CAPACITY_CONNECTOR_GLOBAL_RATE_PER_MINUTE", 1200),
		CapacityConnectorWorkspaceRatePerMinute: intEnv("CAPACITY_CONNECTOR_WORKSPACE_RATE_PER_MINUTE", 300),
		CapacityConnectorProviderRatePerMinute:  intEnv("CAPACITY_CONNECTOR_PROVIDER_RATE_PER_MINUTE", 600),
		CapacityQueueDepthThreshold:             intEnv("CAPACITY_QUEUE_DEPTH_THRESHOLD", 200),
		CapacityQueueOldestAgeThreshold:         durationEnv("CAPACITY_QUEUE_OLDEST_AGE_THRESHOLD", 5*time.Minute),
		BusinessEventReplayLimit:                intEnv("BUSINESS_EVENT_REPLAY_LIMIT", 256),
		BusinessEventSubscriberBuffer:           intEnv("BUSINESS_EVENT_SUBSCRIBER_BUFFER", 32),
		BusinessEventGlobalConnections:          intEnv("BUSINESS_EVENT_GLOBAL_CONNECTIONS", 512),
		BusinessEventWorkspaceConnections:       intEnv("BUSINESS_EVENT_WORKSPACE_CONNECTIONS", 64),
		BusinessEventPrincipalConnections:       intEnv("BUSINESS_EVENT_PRINCIPAL_CONNECTIONS", 8),
		BusinessEventHeartbeatInterval:          durationEnv("BUSINESS_EVENT_HEARTBEAT_INTERVAL", 15*time.Second),
		BusinessEventRetryInterval:              durationEnv("BUSINESS_EVENT_RETRY_INTERVAL", 3*time.Second),
		TelemetryExporter:                       env("TELEMETRY_EXPORTER", env("OTEL_TRACES_EXPORTER", "none")),
		TelemetryEndpoint:                       strings.TrimSpace(env("TELEMETRY_ENDPOINT", os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))),
		TelemetryHeaders:                        keyValueEnv("TELEMETRY_HEADERS", os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
		TelemetryInsecure:                       boolEnv("TELEMETRY_INSECURE", false),
		TelemetrySampleRatio:                    floatEnv("TELEMETRY_SAMPLE_RATIO", 1),
		TelemetryExportTimeout:                  durationEnv("TELEMETRY_EXPORT_TIMEOUT", 5*time.Second),
		HealthCheckTimeout:                      durationEnv("HEALTH_CHECK_TIMEOUT", 2*time.Second),
		DatabaseDriver:                          env("DATABASE_DRIVER", "sqlite"),
		DatabaseDSN:                             env("DATABASE_DSN", ""),
		DatabaseMigrationDSN:                    strings.TrimSpace(os.Getenv("DATABASE_MIGRATION_DSN")),
		DatabaseMigrationMode:                   databaseMigrationModeEnv(environment),
		DatabaseMinSchemaVersion:                strings.TrimSpace(os.Getenv("DATABASE_MIN_SCHEMA_VERSION")),
		DatabaseMaxSchemaVersion:                strings.TrimSpace(os.Getenv("DATABASE_MAX_SCHEMA_VERSION")),
		DatabaseConnectionMode:                  strings.TrimSpace(os.Getenv("DATABASE_CONNECTION_MODE")),
		DatabaseSchema:                          strings.TrimSpace(os.Getenv("DATABASE_SCHEMA")),
		DatabaseMaxOpenConns:                    intEnv("DATABASE_MAX_OPEN_CONNS", 10),
		DatabaseMaxIdleConns:                    intEnv("DATABASE_MAX_IDLE_CONNS", 5),
		DatabaseMaxConnections:                  intEnv("DATABASE_MAX_CONNECTIONS", 0),
		DatabaseReservedConnections:             intEnv("DATABASE_RESERVED_CONNECTIONS", 0),
		RuntimeReplicaCount:                     intEnv("RUNTIME_REPLICA_COUNT", 1),
		DatabaseConnMaxLifetime:                 durationEnv("DATABASE_CONN_MAX_LIFETIME", 30*time.Minute),
		DatabaseConnMaxIdleTime:                 durationEnv("DATABASE_CONN_MAX_IDLE_TIME", 5*time.Minute),
		DatabaseConnectTimeout:                  durationEnv("DATABASE_CONNECT_TIMEOUT", 10*time.Second),
		DatabaseStatementTimeout:                durationEnv("DATABASE_STATEMENT_TIMEOUT", 30*time.Second),
		DatabaseLockTimeout:                     durationEnv("DATABASE_LOCK_TIMEOUT", 5*time.Second),
		DatabaseSSLRootCert:                     strings.TrimSpace(os.Getenv("DATABASE_SSL_ROOT_CERT")),
		DatabaseRLSEnabled:                      boolEnv("DATABASE_RLS_ENABLED", false),
		DBPath:                                  env("APP_DB_PATH", "../data/runtime.db"),
		ManifestPath:                            env("TEMPLATE_MANIFEST", "../domainry.template.json"),
		FrontendCapabilityManifestPath:          strings.TrimSpace(os.Getenv("FRONTEND_CAPABILITY_MANIFEST")),
		MigrationDir:                            env("MIGRATION_DIR", "../migrations"),
		MigrationSQL:                            strings.TrimSpace(os.Getenv("MIGRATION_SQL")),
		MigrationBackupDir:                      env("MIGRATION_BACKUP_DIR", "../data/migration-backups"),
		MigrationBackupEvidencePath:             strings.TrimSpace(os.Getenv("MIGRATION_BACKUP_EVIDENCE_PATH")),
		MigrationBackupLastSuccessAt:            strings.TrimSpace(os.Getenv("MIGRATION_BACKUP_LAST_SUCCESS_AT")),
		MigrationRestoreDrillSuccessAt:          strings.TrimSpace(os.Getenv("MIGRATION_RESTORE_DRILL_LAST_SUCCESS_AT")),
		MigrationOperator:                       env("MIGRATION_OPERATOR", "runtime"),
		MigrationInstanceID:                     strings.TrimSpace(os.Getenv("MIGRATION_INSTANCE_ID")),
		SkipManifestValidation:                  boolEnv("SKIP_MANIFEST_VALIDATION", false),
		BusinessSeedSyncDisabled:                !boolEnv("BUSINESS_SEED_SYNC_ENABLED", true),
		UploadDir:                               env("UPLOAD_DIR", "../data/uploads"),
		CORSAllowedOrigins:                      csvEnv("CORS_ALLOWED_ORIGINS", []string{"*"}),
		SurfaceBusinessOrigins:                  csvEnv("SURFACE_BUSINESS_ORIGINS", nil),
		SurfaceAdminOrigins:                     csvEnv("SURFACE_ADMIN_ORIGINS", nil),
		SurfacePortalOrigins:                    csvEnv("SURFACE_PORTAL_ORIGINS", nil),
		RuntimeAllowDevIdentityHeaders:          boolEnv("RUNTIME_ALLOW_DEV_IDENTITY_HEADERS", false),
		AuditExportTokenKey:                     env("AUDIT_EXPORT_TOKEN_KEY", DevAuditExportTokenKey),
		IdentityRedirectURLs:                    csvEnv("IDENTITY_REDIRECT_URLS", []string{"http://localhost:3100/auth/callback"}),
		IdentityWorkspaceID:                     env("IDENTITY_WORKSPACE_ID", "default"),
		IdentityAudience:                        env("IDENTITY_AUDIENCE", "domainry-runtime"),
		NotificationTenantID:                    env("NOTIFICATION_TENANT_ID", "default"),
		NotificationWorkspaceID:                 env("NOTIFICATION_WORKSPACE_ID", "default"),
		NotificationApplicationKey:              env("NOTIFICATION_APPLICATION_KEY", "domainry-runtime"),
		PartyTenantID:                           env("PARTY_TENANT_ID", "default"),
		PartyWorkspaceID:                        env("PARTY_WORKSPACE_ID", "default"),
		PartyApplicationKey:                     env("PARTY_APPLICATION_KEY", "domainry-runtime"),
		IntegrationSecretKey:                    env("INTEGRATION_SECRET_KEY", DevIntegrationSecret),
		IntegrationActiveKeyID:                  env("INTEGRATION_ACTIVE_KEY_ID", "dev-v1"),
		IntegrationDecryptOnlyKeys:              keyMapEnv("INTEGRATION_DECRYPT_ONLY_KEYS"),
		AgentHTTPBaseURL:                        env("AGENT_HTTP_BASE_URL", "https://integration.domainry.ai"),
		AgentHTTPAPIKey:                         strings.TrimSpace(os.Getenv("AGENT_HTTP_API_KEY")),
		AgentHTTPAgentID:                        intEnv("AGENT_HTTP_AGENT_ID", 0),
		AgentHTTPTimeout:                        durationEnv("AGENT_HTTP_TIMEOUT", 120*time.Second),
		AgentHTTPRateLimitPerMinute:             intEnv("AGENT_HTTP_RATE_LIMIT_PER_MINUTE", 60),
		WorkerPollInterval:                      durationEnv("WORKER_POLL_INTERVAL", durationEnv("SCHEDULER_POLL_INTERVAL", schedulerPollDefault)),
		WorkerBatchSize:                         intEnv("WORKER_BATCH_SIZE", intEnv("SCHEDULER_BATCH_SIZE", 25)),
		SchedulerEnabled:                        boolEnv("SCHEDULER_ENABLED", true),
		SchedulerPollInterval:                   durationEnv("SCHEDULER_POLL_INTERVAL", schedulerPollDefault),
		SchedulerBatchSize:                      intEnv("SCHEDULER_BATCH_SIZE", 25),
		SchedulerLeaseTTL:                       durationEnv("SCHEDULER_LEASE_TTL", 5*time.Minute),
		SchedulerMaxCatchupWindows:              intEnv("SCHEDULER_MAX_CATCHUP_WINDOWS", 1),
	}
}

func (c Config) EffectiveProductBrandName() string {
	return productbrand.ResolveName(c.ProductBrandName)
}

func (c Config) EffectiveWorkerPollInterval() time.Duration {
	if c.WorkerPollInterval > 0 {
		return c.WorkerPollInterval
	}
	return c.SchedulerPollInterval
}

func (c Config) EffectiveWorkerBatchSize() int {
	if c.WorkerBatchSize > 0 {
		return c.WorkerBatchSize
	}
	return c.SchedulerBatchSize
}

func databaseMigrationModeEnv(environment string) string {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("DATABASE_MIGRATION_MODE"))); value != "" {
		return value
	}
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return "verify"
	default:
		return "apply"
	}
}

func (c Config) EffectiveDatabaseMigrationMode() string {
	if value := strings.ToLower(strings.TrimSpace(c.DatabaseMigrationMode)); value != "" {
		return value
	}
	if c.IsProduction() {
		return "verify"
	}
	return "apply"
}

func (c Config) ValidateSecurity() error {
	if !c.IsProduction() {
		return nil
	}
	if c.RuntimeAllowDevIdentityHeaders {
		return fmt.Errorf("RUNTIME_ALLOW_DEV_IDENTITY_HEADERS must be false in production")
	}
	if strings.TrimSpace(c.AuditExportTokenKey) == "" || strings.TrimSpace(c.AuditExportTokenKey) == DevAuditExportTokenKey {
		return fmt.Errorf("AUDIT_EXPORT_TOKEN_KEY must be set to a non-default value in production")
	}
	if (strings.EqualFold(strings.TrimSpace(c.DatabaseDriver), "postgres") || strings.EqualFold(strings.TrimSpace(c.DatabaseDriver), "postgresql")) && !c.DatabaseRLSEnabled {
		return fmt.Errorf("DATABASE_RLS_ENABLED must be true for PostgreSQL in production")
	}
	if strings.TrimSpace(c.IntegrationSecretKey) == "" || strings.TrimSpace(c.IntegrationSecretKey) == DevIntegrationSecret || strings.TrimSpace(c.IntegrationSecretKey) == c.AuditExportTokenKey {
		return fmt.Errorf("INTEGRATION_SECRET_KEY must be set to a non-default value in production")
	}
	if strings.TrimSpace(c.IntegrationActiveKeyID) == "" {
		return fmt.Errorf("INTEGRATION_ACTIVE_KEY_ID must be set in production")
	}
	for _, origin := range c.CORSAllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			return fmt.Errorf("CORS_ALLOWED_ORIGINS must not contain * in production")
		}
	}
	if err := c.validateProductionSurfaceOrigins(); err != nil {
		return err
	}
	if err := c.validateProductionSurfaceListeners(); err != nil {
		return err
	}
	if c.SchedulerEnabled && c.SchedulerPollInterval < time.Second {
		return fmt.Errorf("SCHEDULER_POLL_INTERVAL must be at least 1s in production")
	}
	if strings.TrimSpace(c.TelemetryEndpoint) != "" && c.TelemetryInsecure {
		return fmt.Errorf("TELEMETRY_INSECURE must be false in production")
	}
	return nil
}

func (c Config) validateProductionSurfaceListeners() error {
	listeners := []struct {
		name string
		addr string
	}{
		{"HTTP_PUBLIC_ADDR", c.HTTPPublicAddr},
		{"HTTP_TENANT_ADMIN_ADDR", c.HTTPTenantAdminAddr},
		{"HTTP_OPS_ADDR", c.HTTPOpsAddr},
	}
	seen := map[string]string{}
	for _, listener := range listeners {
		addr := strings.TrimSpace(listener.addr)
		if addr == "" {
			return fmt.Errorf("%s must be set in production", listener.name)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil || strings.TrimSpace(port) == "" {
			return fmt.Errorf("%s must be a valid host:port listener address", listener.name)
		}
		key := strings.ToLower(net.JoinHostPort(strings.Trim(host, "[]"), port))
		if owner := seen[key]; owner != "" {
			return fmt.Errorf("%s must not share listener address %q with %s", listener.name, addr, owner)
		}
		seen[key] = listener.name
	}
	host, _, _ := net.SplitHostPort(strings.TrimSpace(c.HTTPOpsAddr))
	host = strings.Trim(host, "[]")
	private := false
	if ip := net.ParseIP(host); ip != nil {
		private = ip.IsLoopback() || ip.IsPrivate()
	}
	if !private {
		if !c.HTTPOpsAllowPublicBindBreakGlass || strings.TrimSpace(c.HTTPOpsPublicBindBreakGlassReason) == "" {
			return fmt.Errorf("HTTP_OPS_ADDR must bind a loopback/private IP in production unless reviewed break-glass is enabled with a reason")
		}
	}
	return nil
}

func (c Config) validateProductionSurfaceOrigins() error {
	groups := []struct {
		name    string
		origins []string
	}{
		{"SURFACE_BUSINESS_ORIGINS", c.SurfaceBusinessOrigins},
		{"SURFACE_ADMIN_ORIGINS", c.SurfaceAdminOrigins},
		{"SURFACE_PORTAL_ORIGINS", c.SurfacePortalOrigins},
	}
	cors := map[string]bool{}
	for _, origin := range c.CORSAllowedOrigins {
		cors[strings.ToLower(strings.TrimSpace(origin))] = true
	}
	owners := map[string]string{}
	for _, group := range groups {
		if len(group.origins) == 0 {
			return fmt.Errorf("%s must declare at least one exact origin in production", group.name)
		}
		for _, raw := range group.origins {
			origin := strings.TrimSpace(raw)
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
				parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				return fmt.Errorf("%s contains invalid production origin %q", group.name, raw)
			}
			key := strings.ToLower(origin)
			if owner := owners[key]; owner != "" && owner != group.name {
				return fmt.Errorf("Surface origin %q is shared by %s and %s", origin, owner, group.name)
			}
			owners[key] = group.name
			if !cors[key] {
				return fmt.Errorf("%s origin %q is missing from CORS_ALLOWED_ORIGINS", group.name, origin)
			}
		}
	}
	return nil
}

func (c Config) IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(c.Environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}

func (c Config) HTTPAddr() string {
	port := strings.TrimSpace(c.Port)
	if port == "" {
		port = "8081"
	}
	port = strings.TrimPrefix(port, ":")
	host := strings.TrimSpace(c.HTTPBindHost)
	if host == "" {
		return ":" + port
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
