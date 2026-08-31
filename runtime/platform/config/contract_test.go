package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefinitionsCoverEveryConfigField(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != reflect.TypeOf(Config{}).NumField() {
		t.Fatalf("schema drift: definitions=%d config_fields=%d", len(definitions), reflect.TypeOf(Config{}).NumField())
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if definition.Name == "" || definition.Owner == "" || definition.Type == "" {
			t.Fatalf("incomplete definition: %#v", definition)
		}
		if seen[definition.Name] {
			t.Fatalf("duplicate external config name %s", definition.Name)
		}
		seen[definition.Name] = true
	}
}

func TestFromEnvAllowsBusinessSeedSynchronizationToBeDisabledForCleanRoomAcceptance(t *testing.T) {
	t.Setenv("BUSINESS_SEED_SYNC_ENABLED", "false")
	if cfg := FromEnv(); !cfg.BusinessSeedSyncDisabled {
		t.Fatal("business seed synchronization was not disabled")
	}
	t.Setenv("BUSINESS_SEED_SYNC_ENABLED", "true")
	if cfg := FromEnv(); cfg.BusinessSeedSyncDisabled {
		t.Fatal("business seed synchronization did not retain its default-on behavior")
	}
}

func TestLoadWithProjectFileSupportsBusinessSeedSynchronizationSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"BUSINESS_SEED_SYNC_ENABLED":"false"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNTIME_CONFIG_FILE", "")
	t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", "")
	cfg, snapshot, err := LoadWithProjectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BusinessSeedSyncDisabled {
		t.Fatal("project configuration did not disable business seed synchronization")
	}
	entry := snapshot.Entries["BUSINESS_SEED_SYNC_ENABLED"]
	if entry.Source != "project-configuration" {
		t.Fatalf("provenance=%+v", entry)
	}

	t.Setenv("BUSINESS_SEED_SYNC_ENABLED", "true")
	cfg, snapshot, err = LoadWithProjectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BusinessSeedSyncDisabled {
		t.Fatal("environment override did not re-enable business seed synchronization")
	}
	if snapshot.Entries["BUSINESS_SEED_SYNC_ENABLED"].Source != "environment" {
		t.Fatalf("provenance=%+v", snapshot.Entries["BUSINESS_SEED_SYNC_ENABLED"])
	}
}

func TestLoadWithProjectFileUsesDefaultProjectOperatorPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"APP_LOCALE":"zh-CN","PORT":":4567"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNTIME_CONFIG_FILE", "")
	t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", "")
	t.Setenv("PORT", ":5678")
	cfg, snapshot, err := LoadWithProjectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppLocale != "zh-CN" || cfg.Port != ":5678" {
		t.Fatalf("config=%+v", cfg)
	}
	if snapshot.Entries["APP_LOCALE"].Source != "project-configuration" || snapshot.Entries["PORT"].Source != "environment" {
		t.Fatalf("provenance=%+v", snapshot.Entries)
	}
	if _, defaults, err := LoadWithProjectFile(filepath.Join(t.TempDir(), "absent.json")); err != nil || defaults.Entries["APP_LOCALE"].Source != "default" {
		t.Fatalf("absent project extension snapshot=%+v err=%v", defaults, err)
	}
}

func TestLoadWithProjectFileRejectsUnknownSecretAndSymlink(t *testing.T) {
	for name, document := range map[string]struct {
		document string
		expected string
	}{
		"unknown": {`{"NOT_A_RUNTIME_SETTING":"x"}`, "unknown configuration"},
		"secret":  {`{"AUDIT_EXPORT_TOKEN_KEY":"must-not-live-in-git"}`, "forbidden in Git-owned"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.json")
			if err := os.WriteFile(path, []byte(document.document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadWithProjectFile(path); err == nil || !strings.Contains(err.Error(), document.expected) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	target := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadWithProjectFile(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink error=%v", err)
	}
}

func TestOptionalProjectConfigFileRejectsUnreadableMalformedAndTrailingDocuments(t *testing.T) {
	for name, content := range map[string]string{
		"malformed": `{`,
		"trailing":  `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readOptionalProjectConfigFile(path); err == nil {
				t.Fatalf("%s project configuration was accepted", name)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, _, err := readOptionalProjectConfigFile(path); err == nil || !strings.Contains(err.Error(), "read project configuration") {
		t.Fatalf("unreadable project configuration error=%v", err)
	}
}

func TestLoadContractUsesExplicitPriorityAndProvenance(t *testing.T) {
	cfg, snapshot, err := LoadContract(
		Source{Name: "file", Version: "file-7", Priority: 100, Values: map[string]string{"PORT": "8082", "SCHEDULER_BATCH_SIZE": "10"}},
		Source{Name: "environment", Version: "deploy-4", Priority: 300, Values: map[string]string{"PORT": "9090"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "9090" || cfg.SchedulerBatchSize != 10 {
		t.Fatalf("priority not applied: %#v", cfg)
	}
	if snapshot.Entries["PORT"].Source != "environment" || snapshot.Entries["PORT"].Version != "deploy-4" {
		t.Fatalf("provenance missing: %#v", snapshot.Entries["PORT"])
	}
	if snapshot.Revision == "" {
		t.Fatal("snapshot revision missing")
	}
}

func TestLoadContractKeepsSchedulerAndRuntimeWorkerSettingsIndependent(t *testing.T) {
	schedulerOnly, schedulerSnapshot, err := LoadContract(Source{Name: "scheduler-owner", Values: map[string]string{
		"SCHEDULER_POLL_INTERVAL": "3s", "SCHEDULER_BATCH_SIZE": "17",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if schedulerOnly.SchedulerPollInterval != 3*time.Second || schedulerOnly.SchedulerBatchSize != 17 ||
		schedulerOnly.EffectiveWorkerPollInterval() != 500*time.Millisecond || schedulerOnly.EffectiveWorkerBatchSize() != 25 ||
		schedulerSnapshot.Entries["WORKER_POLL_INTERVAL"].Source != "default" || schedulerSnapshot.Entries["WORKER_BATCH_SIZE"].Source != "default" {
		t.Fatalf("scheduler settings leaked into Runtime worker=%+v snapshot=%+v", schedulerOnly, schedulerSnapshot.Entries)
	}
	explicit, snapshot, err := LoadContract(Source{Name: "runtime-worker-owner", Values: map[string]string{
		"SCHEDULER_POLL_INTERVAL": "4s", "SCHEDULER_BATCH_SIZE": "19",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_BATCH_SIZE": "23",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.SchedulerPollInterval != 4*time.Second || explicit.SchedulerBatchSize != 19 ||
		explicit.EffectiveWorkerPollInterval() != 250*time.Millisecond || explicit.EffectiveWorkerBatchSize() != 23 ||
		snapshot.Entries["WORKER_POLL_INTERVAL"].Source != "runtime-worker-owner" {
		t.Fatalf("explicit worker routing=%+v snapshot=%+v", explicit, snapshot.Entries)
	}
}

func TestLoadContractKeepsRecordTimerConfigurationIndependentFromScheduler(t *testing.T) {
	cfg, snapshot, err := LoadContract(
		Source{Name: "scheduler-owner", Values: map[string]string{
			"SCHEDULER_ENABLED":       "false",
			"SCHEDULER_POLL_INTERVAL": "4s",
			"SCHEDULER_BATCH_SIZE":    "19",
		}},
		Source{Name: "record-timer-owner", Values: map[string]string{
			"RECORD_TIMER_ENABLED":       "true",
			"RECORD_TIMER_POLL_INTERVAL": "2s",
			"RECORD_TIMER_BATCH_SIZE":    "7",
			"RECORD_TIMER_LEASE_TTL":     "20s",
		}},
		Source{Name: "runtime-worker-owner", Values: map[string]string{
			"WORKER_POLL_INTERVAL": "250ms",
			"WORKER_BATCH_SIZE":    "23",
			"WORKER_LEASE_TTL":     "30s",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerEnabled || !cfg.RecordTimerEnabled || cfg.RecordTimerPollInterval != 2*time.Second || cfg.RecordTimerBatchSize != 7 || cfg.RecordTimerLeaseTTL != 20*time.Second {
		t.Fatalf("record timer configuration coupled to scheduler: %+v", cfg)
	}
	if snapshot.Entries["SCHEDULER_POLL_INTERVAL"].Source != "scheduler-owner" || snapshot.Entries["RECORD_TIMER_POLL_INTERVAL"].Source != "record-timer-owner" || snapshot.Entries["WORKER_POLL_INTERVAL"].Source != "runtime-worker-owner" {
		t.Fatalf("record timer provenance=%+v", snapshot.Entries)
	}
}

func TestLoadContractRejectsTyposAndInvalidCombinations(t *testing.T) {
	if _, _, err := LoadContract(Source{Name: "environment", Values: map[string]string{"AUDIT_EXPORT_TOKEN_KE": "typo"}}); err == nil {
		t.Fatal("unknown configuration was silently accepted")
	}
	if _, _, err := LoadContract(Source{Name: "environment", Values: map[string]string{"IDENTITY_MODE": "saas"}}); err == nil {
		t.Fatal("Identity deployment topology leaked into the Runtime configuration contract")
	}
	if _, _, err := LoadContract(Source{Name: "environment", Values: map[string]string{"HTTP_READ_TIMEOUT": "not-a-duration"}}); err == nil {
		t.Fatal("invalid duration silently fell back")
	}
}

func TestProductionSecurityRejectsSharedAuditAndIntegrationKeys(t *testing.T) {
	cfg := Config{Environment: "production", AuditExportTokenKey: "shared", IntegrationSecretKey: "shared", IntegrationActiveKeyID: "key-1", CORSAllowedOrigins: []string{"https://admin.example.com"}, SchedulerPollInterval: time.Second}
	setValidProductionListenerOrigins(&cfg)
	if err := cfg.ValidateSecurity(); err == nil {
		t.Fatal("production accepted one key for audit export and integration encryption")
	}
}

func TestLoadRejectsMisspelledManagedEnvironmentVariable(t *testing.T) {
	t.Setenv("CONFIG_UNKNOWN_POLICY", "error")
	t.Setenv("AUDIT_EXPORT_TOKEN_KE", "typo")
	if _, _, err := Load(); err == nil {
		t.Fatal("misspelled managed environment variable was ignored")
	}
}

func TestProductionSecurityHardGates(t *testing.T) {
	valid := Config{Environment: "production", AuditExportTokenKey: "audit-export", IntegrationSecretKey: "integration", IntegrationActiveKeyID: "data-1", CORSAllowedOrigins: []string{"https://admin.example.com"}, SchedulerPollInterval: time.Second}
	setValidProductionListenerOrigins(&valid)
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"default audit export key", func(cfg *Config) { cfg.AuditExportTokenKey = DevAuditExportTokenKey }},
		{"default integration key", func(cfg *Config) { cfg.IntegrationSecretKey = DevIntegrationSecret }},
		{"open CORS", func(cfg *Config) { cfg.CORSAllowedOrigins = []string{"*"} }},
		{"dev headers", func(cfg *Config) { cfg.RuntimeAllowDevIdentityHeaders = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.ValidateSecurity(); err == nil {
				t.Fatalf("production accepted %s", test.name)
			}
		})
	}
}
