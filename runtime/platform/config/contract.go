package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ValueType string

const (
	TypeString   ValueType = "string"
	TypeBool     ValueType = "bool"
	TypeInt      ValueType = "int"
	TypeFloat    ValueType = "float"
	TypeDuration ValueType = "duration"
	TypeCSV      ValueType = "csv"
	TypeKeyMap   ValueType = "key-map"
)

type Definition struct {
	Field    string    `json:"field"`
	Name     string    `json:"name"`
	Owner    string    `json:"owner"`
	Type     ValueType `json:"type"`
	Required bool      `json:"required"`
	Default  any       `json:"default,omitempty"`
	Secret   bool      `json:"secret"`
}

type Provenance struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Version  string `json:"version"`
	Redacted bool   `json:"redacted"`
}

type Snapshot struct {
	Revision string                `json:"revision"`
	Entries  map[string]Provenance `json:"entries"`
	Warnings []string              `json:"warnings,omitempty"`
}

type Source struct {
	Name     string
	Version  string
	Priority int
	Values   map[string]string
}

// Definitions is the executable configuration schema. Reflection derives the
// scalar type, while this table owns external names and security metadata.
func Definitions() []Definition {
	defaults := FromEnv()
	typeOfConfig := reflect.TypeOf(defaults)
	valueOfDefaults := reflect.ValueOf(defaults)
	out := make([]Definition, 0, typeOfConfig.NumField())
	for i := 0; i < typeOfConfig.NumField(); i++ {
		field := typeOfConfig.Field(i)
		name := configEnvName(field.Name)
		secret := strings.Contains(name, "SECRET") || strings.Contains(name, "PASSWORD") || strings.Contains(name, "TOKEN") || strings.Contains(name, "DSN") || strings.Contains(name, "API_KEY") || strings.Contains(name, "ACCESS_TOKEN") || field.Name == "IntegrationDecryptOnlyKeys" || field.Name == "TelemetryHeaders" || field.Name == "RateLimitRedisURL"
		defaultValue := valueOfDefaults.Field(i).Interface()
		if field.Name == "BusinessSeedSyncDisabled" {
			defaultValue = !defaultValue.(bool)
		}
		if secret {
			defaultValue = "[REDACTED]"
		}
		definition := Definition{Field: field.Name, Name: name, Owner: configOwner(name), Type: reflectValueType(field.Type), Default: defaultValue, Secret: secret}
		definition.Required = name == "AUDIT_EXPORT_TOKEN_KEY" || name == "INTEGRATION_SECRET_KEY"
		out = append(out, definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LoadContract merges defaults < file/provider < environment < remote. It is
// also useful in tests and production adapters because no source is global.
func LoadContract(sources ...Source) (Config, Snapshot, error) {
	cfg := FromEnv()
	definitions := Definitions()
	byName := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Priority < sources[j].Priority })
	entries := map[string]Provenance{}
	for _, definition := range definitions {
		entries[definition.Name] = Provenance{Name: definition.Name, Source: "default", Version: valueVersion(definition.Default), Redacted: definition.Secret}
	}
	for _, source := range sources {
		for name, raw := range source.Values {
			definition, ok := byName[name]
			if !ok {
				return Config{}, Snapshot{}, fmt.Errorf("unknown configuration %s from %s", name, source.Name)
			}
			if name == "WORKSPACE_PROVISION_FAILURE_POINT" && source.Name != "environment" {
				return Config{}, Snapshot{}, fmt.Errorf("WORKSPACE_PROVISION_FAILURE_POINT is process-environment-only and cannot be loaded from %s", source.Name)
			}
			if err := setConfigField(&cfg, definition, raw); err != nil {
				return Config{}, Snapshot{}, fmt.Errorf("invalid %s from %s: %w", name, source.Name, err)
			}
			version := strings.TrimSpace(source.Version)
			if version == "" {
				if definition.Secret {
					version = "unversioned"
				} else {
					version = valueVersion(raw)
				}
			}
			entries[name] = Provenance{Name: name, Source: source.Name, Version: version, Redacted: definition.Secret}
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, Snapshot{}, err
	}
	revision := snapshotRevision(entries)
	return cfg, Snapshot{Revision: revision, Entries: entries}, nil
}

func Load() (Config, Snapshot, error) {
	return LoadWithProjectFile("")
}

// LoadWithProjectFile loads one Git-owned project extension below Runtime
// defaults and all operator-owned sources. The project file may override only
// known, non-secret settings; an absent file means use Runtime defaults.
func LoadWithProjectFile(projectFile string) (Config, Snapshot, error) {
	environmentValues, fileValues := map[string]string{}, map[string]string{}
	projectValues, projectVersion, err := readOptionalProjectConfigFile(projectFile)
	if err != nil {
		return Config{}, Snapshot{}, err
	}
	staticFileValues, staticFileVersion, err := readConfigSourceFile("RUNTIME_CONFIG_FILE")
	if err != nil {
		return Config{}, Snapshot{}, err
	}
	remoteValues, remoteVersion, err := readConfigSourceFile("RUNTIME_REMOTE_CONFIG_FILE")
	if err != nil {
		return Config{}, Snapshot{}, err
	}
	definitions := Definitions()
	known := map[string]bool{}
	for _, definition := range definitions {
		known[definition.Name] = true
		if value, ok := os.LookupEnv(definition.Name); ok {
			environmentValues[definition.Name] = value
		}
		if definition.Secret {
			if path := strings.TrimSpace(os.Getenv(definition.Name + "_FILE")); path != "" {
				material, err := os.ReadFile(path)
				if err != nil {
					return Config{}, Snapshot{}, fmt.Errorf("read %s_FILE: %w", definition.Name, err)
				}
				fileValues[definition.Name] = strings.TrimSpace(string(material))
			}
		}
	}
	for name := range projectValues {
		definition, ok := definitionByName(definitions, name)
		if !ok {
			return Config{}, Snapshot{}, fmt.Errorf("unknown configuration %s from project-configuration", name)
		}
		if definition.Secret {
			return Config{}, Snapshot{}, fmt.Errorf("secret configuration %s is forbidden in Git-owned project configuration", name)
		}
	}
	unknown := []string{}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		base := strings.TrimSuffix(name, "_FILE")
		if managedConfigName(name) && !known[base] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	cfg, snapshot, err := LoadContract(
		Source{Name: "project-configuration", Version: projectVersion, Priority: 50, Values: projectValues},
		Source{Name: "configuration-file", Version: staticFileVersion, Priority: 100, Values: staticFileValues},
		Source{Name: "secret-file", Priority: 200, Values: fileValues},
		Source{Name: "environment", Version: strings.TrimSpace(os.Getenv("RUNTIME_CONFIG_VERSION")), Priority: 300, Values: environmentValues},
		Source{Name: "remote-configuration", Version: remoteVersion, Priority: 400, Values: remoteValues},
	)
	if err != nil {
		return Config{}, Snapshot{}, err
	}
	if len(unknown) > 0 {
		message := "unknown Runtime configuration: " + strings.Join(unknown, ", ")
		if cfg.IsProduction() || strings.EqualFold(strings.TrimSpace(os.Getenv("CONFIG_UNKNOWN_POLICY")), "error") {
			return Config{}, Snapshot{}, fmt.Errorf("%s", message)
		}
		snapshot.Warnings = append(snapshot.Warnings, message)
	}
	return cfg, snapshot, nil
}

func definitionByName(definitions []Definition, name string) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return Definition{}, false
}

func (s Snapshot) StartupReport() []Provenance {
	out := make([]Provenance, 0, len(s.Entries))
	for _, entry := range s.Entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c Config) Validate() error {
	if err := c.ValidateSecurity(); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(c.RateLimitBackend)) {
	case "", "database":
	case "redis":
		if strings.TrimSpace(c.RateLimitRedisURL) == "" {
			return errors.New("RATE_LIMIT_REDIS_URL is required when RATE_LIMIT_BACKEND=redis")
		}
		if c.RateLimitRedisConnectTimeout <= 0 {
			return errors.New("RATE_LIMIT_REDIS_CONNECT_TIMEOUT must be positive")
		}
	default:
		return fmt.Errorf("unsupported RATE_LIMIT_BACKEND %q", c.RateLimitBackend)
	}
	if strings.TrimSpace(c.IdentityAudience) == "" {
		return fmt.Errorf("IDENTITY_AUDIENCE is required")
	}
	if strings.TrimSpace(c.NotificationApplicationKey) == "" {
		return fmt.Errorf("NOTIFICATION_APPLICATION_KEY is required")
	}
	for name, value := range map[string]string{
		"IDENTITY_WORKSPACE_ID": c.IdentityWorkspaceID, "NOTIFICATION_TENANT_ID": c.NotificationTenantID,
		"NOTIFICATION_WORKSPACE_ID": c.NotificationWorkspaceID,
	} {
		if strings.EqualFold(strings.TrimSpace(value), "default") {
			return fmt.Errorf("%s cannot use the reserved default tenant", name)
		}
	}
	if c.HTTPReadHeaderTimeout <= 0 || c.HTTPReadTimeout <= 0 || c.HTTPWriteTimeout <= 0 || c.HTTPIdleTimeout <= 0 || c.HTTPShutdownTimeout <= 0 {
		return fmt.Errorf("HTTP timeouts must be positive")
	}
	if c.SchedulerBatchSize < 1 || c.SchedulerBatchSize > 1000 {
		return fmt.Errorf("SCHEDULER_BATCH_SIZE must be between 1 and 1000")
	}
	if c.RecordTimerBatchSize < 1 || c.RecordTimerBatchSize > 1000 {
		return fmt.Errorf("RECORD_TIMER_BATCH_SIZE must be between 1 and 1000")
	}
	if c.EffectiveWorkerBatchSize() < 1 || c.EffectiveWorkerBatchSize() > 1000 {
		return fmt.Errorf("WORKER_BATCH_SIZE must be between 1 and 1000")
	}
	if c.EffectiveWorkerPollInterval() <= 0 {
		return fmt.Errorf("WORKER_POLL_INTERVAL must be positive")
	}
	if c.SchedulerLeaseTTL <= c.SchedulerPollInterval {
		return fmt.Errorf("SCHEDULER_LEASE_TTL must exceed SCHEDULER_POLL_INTERVAL")
	}
	if c.RecordTimerLeaseTTL <= c.RecordTimerPollInterval {
		return fmt.Errorf("RECORD_TIMER_LEASE_TTL must exceed RECORD_TIMER_POLL_INTERVAL")
	}
	if c.WorkerLeaseTTL <= c.EffectiveWorkerPollInterval() {
		return fmt.Errorf("WORKER_LEASE_TTL must exceed WORKER_POLL_INTERVAL")
	}
	if strings.TrimSpace(c.Port) == "" {
		return fmt.Errorf("PORT is required")
	}
	port := strings.TrimPrefix(strings.TrimSpace(c.Port), ":")
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535")
	}
	if host := strings.Trim(strings.TrimSpace(c.HTTPBindHost), "[]"); host != "" &&
		!strings.EqualFold(host, "localhost") && net.ParseIP(host) == nil {
		return fmt.Errorf("HTTP_BIND_HOST must be localhost or an IP address")
	}
	if c.HTTPMaxJSONBodyBytes < 1024 || c.HTTPMaxJSONBodyBytes > 64<<20 {
		return fmt.Errorf("HTTP_MAX_JSON_BODY_BYTES must be between 1KiB and 64MiB")
	}
	if c.HTTPMaxHeaderBytes < 8<<10 || c.HTTPMaxHeaderBytes > 4<<20 {
		return fmt.Errorf("HTTP_MAX_HEADER_BYTES must be between 8KiB and 4MiB")
	}
	if c.CapacityGlobalInFlight < 1 || c.CapacityGlobalInFlight > 100_000 {
		return fmt.Errorf("CAPACITY_GLOBAL_IN_FLIGHT must be between 1 and 100000")
	}
	if c.CapacityWorkspaceInFlight < 1 || c.CapacityWorkspaceInFlight > c.CapacityGlobalInFlight {
		return fmt.Errorf("CAPACITY_WORKSPACE_IN_FLIGHT must be positive and not exceed global capacity")
	}
	if c.CapacityUseCaseInFlight < 1 || c.CapacityUseCaseInFlight > c.CapacityGlobalInFlight {
		return fmt.Errorf("CAPACITY_USE_CASE_IN_FLIGHT must be positive and not exceed global capacity")
	}
	if c.CapacityRetryInFlight < 1 || c.CapacityRetryInFlight >= c.CapacityGlobalInFlight {
		return fmt.Errorf("CAPACITY_RETRY_IN_FLIGHT must reserve capacity for normal requests")
	}
	if c.CapacityGlobalRatePerMinute < 1 || c.CapacityGlobalRatePerMinute > 10_000_000 || c.CapacityWorkspaceRatePerMinute < 1 || c.CapacityWorkspaceRatePerMinute > c.CapacityGlobalRatePerMinute || c.CapacityUseCaseRatePerMinute < 1 || c.CapacityUseCaseRatePerMinute > c.CapacityGlobalRatePerMinute {
		return fmt.Errorf("capacity rate budgets must be positive and workspace/use-case must not exceed global")
	}
	if c.CapacityMaxWorkspaceStates < 1 || c.CapacityMaxUseCaseStates < 1 || c.CapacityWorkspaceStateTTL <= 0 || c.CapacityRequestTimeout <= 0 || c.CapacityRetryAfter <= 0 {
		return fmt.Errorf("capacity state, TTL, timeout, and retry-after must be positive")
	}
	if c.CapacityDegradedRatio <= 0 || c.CapacityDegradedRatio >= 1 || c.CapacityRecoveryRatio <= 0 || c.CapacityRecoveryRatio >= c.CapacityDegradedRatio {
		return fmt.Errorf("capacity hysteresis ratios are invalid")
	}
	if c.CapacityConnectorGlobalInFlight < 1 || c.CapacityConnectorWorkspaceInFlight < 1 || c.CapacityConnectorWorkspaceInFlight > c.CapacityConnectorGlobalInFlight || c.CapacityConnectorProviderInFlight < 1 || c.CapacityConnectorProviderInFlight > c.CapacityConnectorGlobalInFlight {
		return fmt.Errorf("connector in-flight capacity hierarchy is invalid")
	}
	if c.CapacityConnectorGlobalRatePerMinute < 1 || c.CapacityConnectorWorkspaceRatePerMinute < 1 || c.CapacityConnectorWorkspaceRatePerMinute > c.CapacityConnectorGlobalRatePerMinute || c.CapacityConnectorProviderRatePerMinute < 1 || c.CapacityConnectorProviderRatePerMinute > c.CapacityConnectorGlobalRatePerMinute {
		return fmt.Errorf("connector rate capacity hierarchy is invalid")
	}
	if c.CapacityQueueDepthThreshold < 1 || c.CapacityQueueDepthThreshold > 1_000_000 || c.CapacityQueueOldestAgeThreshold <= 0 {
		return fmt.Errorf("queue backpressure thresholds are invalid")
	}
	if c.BusinessEventReplayLimit < 1 || c.BusinessEventReplayLimit > 100_000 || c.BusinessEventSubscriberBuffer < 1 || c.BusinessEventSubscriberBuffer > 10_000 {
		return fmt.Errorf("business event replay and subscriber buffer limits are invalid")
	}
	if c.BusinessEventGlobalConnections < 1 || c.BusinessEventGlobalConnections > 100_000 || c.BusinessEventWorkspaceConnections < 1 || c.BusinessEventWorkspaceConnections > c.BusinessEventGlobalConnections || c.BusinessEventPrincipalConnections < 1 || c.BusinessEventPrincipalConnections > c.BusinessEventWorkspaceConnections {
		return fmt.Errorf("business event connection limits are invalid")
	}
	if c.BusinessEventHeartbeatInterval < time.Second || c.BusinessEventHeartbeatInterval > time.Minute || c.BusinessEventRetryInterval < 250*time.Millisecond || c.BusinessEventRetryInterval > time.Minute {
		return fmt.Errorf("business event heartbeat and retry intervals are invalid")
	}
	if c.TelemetrySampleRatio < 0 || c.TelemetrySampleRatio > 1 {
		return fmt.Errorf("TELEMETRY_SAMPLE_RATIO must be between 0 and 1")
	}
	if c.DatabaseMaxOpenConns < 1 || c.DatabaseMaxIdleConns < 0 || c.DatabaseMaxIdleConns > c.DatabaseMaxOpenConns {
		return fmt.Errorf("database pool sizes are inconsistent")
	}
	if c.DatabaseConnMaxLifetime <= 0 || c.DatabaseConnMaxIdleTime <= 0 || c.DatabaseConnectTimeout <= 0 || c.DatabaseStatementTimeout <= 0 || c.DatabaseLockTimeout <= 0 {
		return fmt.Errorf("database timeouts must be positive")
	}
	if strings.TrimSpace(c.ManifestPath) == "" || strings.TrimSpace(c.MigrationDir) == "" || strings.TrimSpace(c.UploadDir) == "" {
		return fmt.Errorf("manifest, migration, and upload paths are required")
	}
	return nil
}

func setConfigField(cfg *Config, definition Definition, raw string) error {
	field := reflect.ValueOf(cfg).Elem().FieldByName(definition.Field)
	switch definition.Type {
	case TypeString:
		field.SetString(strings.TrimSpace(raw))
	case TypeBool:
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		if definition.Field == "BusinessSeedSyncDisabled" {
			value = !value
		}
		field.SetBool(value)
	case TypeInt:
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		field.SetInt(int64(value))
	case TypeFloat:
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return err
		}
		field.SetFloat(value)
	case TypeDuration:
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		field.SetInt(int64(value))
	case TypeCSV:
		field.Set(reflect.ValueOf(splitCSV(raw)))
	case TypeKeyMap:
		field.Set(reflect.ValueOf(parseKeyMap(raw)))
	default:
		return fmt.Errorf("unsupported type %s", definition.Type)
	}
	return nil
}

func configEnvName(field string) string {
	overrides := map[string]string{"RuntimeVersion": "DOMAINRY_RUNTIME_VERSION", "Environment": "APP_ENV", "AppLocale": "APP_LOCALE", "DatabaseDSN": "DATABASE_DSN", "DBPath": "APP_DB_PATH", "ManifestPath": "TEMPLATE_MANIFEST", "MigrationSQL": "MIGRATION_SQL", "MigrationRestoreDrillSuccessAt": "MIGRATION_RESTORE_DRILL_LAST_SUCCESS_AT", "SkipManifestValidation": "SKIP_MANIFEST_VALIDATION", "BusinessSeedSyncDisabled": "BUSINESS_SEED_SYNC_ENABLED", "Port": "PORT"}
	if value := overrides[field]; value != "" {
		return value
	}
	runes := []rune(field)
	var out strings.Builder
	for index, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if index > 0 && upper {
			previous := runes[index-1]
			previousLowerOrDigit := previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9'
			nextLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			if previousLowerOrDigit || nextLower {
				out.WriteByte('_')
			}
		}
		out.WriteRune(r)
	}
	return strings.ToUpper(out.String())
}

func configOwner(name string) string {
	parts := strings.Split(name, "_")
	if len(parts) > 1 {
		return strings.ToLower(parts[0])
	}
	return "runtime"
}
func reflectValueType(value reflect.Type) ValueType {
	if value == reflect.TypeOf(time.Duration(0)) {
		return TypeDuration
	}
	switch value.Kind() {
	case reflect.Bool:
		return TypeBool
	case reflect.Int:
		return TypeInt
	case reflect.Float64:
		return TypeFloat
	case reflect.Slice:
		return TypeCSV
	case reflect.Map:
		return TypeKeyMap
	default:
		return TypeString
	}
}
func splitCSV(raw string) []string {
	out := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
func parseKeyMap(raw string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.TrimSpace(key) != "" {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}
func valueVersion(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}
func snapshotRevision(entries map[string]Provenance) string {
	encoded, _ := json.Marshal(entries)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func managedConfigName(name string) bool {
	if name == "RUNTIME_CONFIG_FILE" || name == "RUNTIME_REMOTE_CONFIG_FILE" || name == "RUNTIME_CONFIG_VERSION" {
		return false
	}
	if name == "PORT" {
		return true
	}
	for _, prefix := range []string{"APP_", "AUTH_", "AUDIT_", "IDENTITY_", "NOTIFICATION_", "HTTP_", "HEALTH_", "CAPACITY_", "TELEMETRY_", "DATABASE_", "RUNTIME_", "MIGRATION_", "SCHEDULER_", "RECORD_TIMER_", "WORKER_", "BUSINESS_", "AGENT_DIALOG_", "CORS_", "INTEGRATION_", "TEMPLATE_", "SKIP_MANIFEST_", "UPLOAD_", "DOMAINRY_RUNTIME_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var configSourceStat = os.Stat

func readConfigSourceFile(environmentName string) (map[string]string, string, error) {
	path := strings.TrimSpace(os.Getenv(environmentName))
	if path == "" {
		return map[string]string{}, "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", environmentName, err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", environmentName, err)
	}
	info, err := configSourceStat(path)
	if err != nil {
		return nil, "", fmt.Errorf("stat %s: %w", environmentName, err)
	}
	return values, fmt.Sprintf("%d:%d", info.ModTime().UTC().UnixNano(), info.Size()), nil
}

var optionalProjectConfigLstat = os.Lstat

func readOptionalProjectConfigFile(path string) (map[string]string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]string{}, "", nil
	}
	info, err := optionalProjectConfigLstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("project configuration must be a regular non-symlink file: %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("project configuration must be a regular non-symlink file: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("project configuration must be a regular non-symlink file: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read project configuration: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var values map[string]string
	if err := decoder.Decode(&values); err != nil {
		return nil, "", fmt.Errorf("decode project configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, "", fmt.Errorf("decode project configuration: expected one JSON document")
	}
	return values, valueVersion(values), nil
}
