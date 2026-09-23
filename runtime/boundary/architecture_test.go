package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const runtimeModulePath = "github.com/domainry/domainry-runtime/runtime/"

func TestRuntimeDDDLayerImportBoundary(t *testing.T) {
	forbidden := map[string][]string{
		"domain":      {"application/", "bootstrap/", "infrastructure/", "transport/"},
		"application": {"bootstrap/", "infrastructure/", "transport/"},
		"transport":   {"bootstrap/", "infrastructure/"},
	}
	violations := []string{}
	walkProductionGo(t, runtimeRoot(t), func(path string, file *ast.File) {
		relative, err := filepath.Rel(runtimeRoot(t), path)
		if err != nil {
			t.Fatal(err)
		}
		layer := strings.Split(filepath.ToSlash(relative), "/")[0]
		for _, specification := range file.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil || !strings.HasPrefix(importPath, runtimeModulePath) {
				continue
			}
			dependency := strings.TrimPrefix(importPath, runtimeModulePath)
			for _, prefix := range forbidden[layer] {
				if strings.HasPrefix(dependency, prefix) {
					violations = append(violations, filepath.ToSlash(relative)+" -> "+importPath)
				}
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("DDD layer import violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestRuntimePlatformContainsOnlyHostOwnedBindings(t *testing.T) {
	allowed := map[string]bool{"config": true, "localization": true, "productbrand": true}
	entries, err := os.ReadDir(filepath.Join(runtimeRoot(t), "platform"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && !allowed[entry.Name()] {
			t.Errorf("Runtime Platform capability %q must move to Foundation or its source owner", entry.Name())
		}
	}
}

func TestRuntimeProductionUsesExtractedOwnerSDKs(t *testing.T) {
	forbiddenOwners := []string{
		"github.com/domainry/domainry-data-exchange",
		"github.com/domainry/domainry-integration",
		"github.com/domainry/domainry-notification",
		"github.com/domainry/domainry-party",
	}
	walkProductionGo(t, runtimeRoot(t), func(path string, file *ast.File) {
		for _, specification := range file.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, owner := range forbiddenOwners {
				if importPath == owner || strings.HasPrefix(importPath, owner+"/") {
					t.Errorf("Runtime production imports extracted owner implementation %q in %s", importPath, path)
				}
			}
		}
	})

	for _, relative := range []string{
		"application/auth", "application/identity", "application/party",
		"domain/auth", "domain/identity", "domain/notification", "domain/party",
		"infrastructure/persistence/database/auth", "infrastructure/persistence/database/identity", "infrastructure/persistence/database/party",
		"transport/http/auth", "transport/http/identity", "transport/http/party",
	} {
		path := filepath.Join(runtimeRoot(t), filepath.FromSlash(relative))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			t.Errorf("extracted owner implementation returned to Runtime: %s", relative)
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestRuntimeDoesNotReclaimIntegrationOwnerState(t *testing.T) {
	root := runtimeRoot(t)
	for _, relative := range []string{
		"application/integration",
		"domain/integration",
		"infrastructure/persistence/database/integration",
		"infrastructure/persistence/database/integrationnotification",
		"infrastructure/persistence/database/publicationmetrics",
		"transport/http/integration",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			t.Errorf("Integration owner implementation returned to Runtime: %s", relative)
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	forbiddenFragments := map[string]string{
		"_application_schema_connector_requirements": "retired Connector catalog table",
		"_integration_connector_definitions":         "retired private Connector definition table",
		"_integration_event_mapping_definitions":     "retired private event-mapping definition table",
		"_integration_connector_provider_states":     "retired private Provider state table",
		"_integration_connector_provider_commits":    "retired private Provider commit table",
		"_integration_event_mapping_intents":         "retired private event execution projection table",
		"_integration_credential_refresh_leases":     "retired unused credential refresh lease table",
		"_integration_provider_runs":                 "Integration-owned Provider run table",
		"_integration_connections":                   "Integration-owned connection table",
		"_integration_secrets":                       "Integration-owned secret table",
		"_integration_api_keys":                      "retired private API key table",
		"_integration_external_identities":           "Integration-owned external identity table",
		"_integration_webhook_subscriptions":         "Integration-owned webhook subscription table",
		"_integration_web_push_subscriptions":        "Integration-owned Web Push subscription table",
		"_integration_invocations":                   "Integration-owned invocation table",
		"_integration_events":                        "Integration-owned event table",
		"integration.outbox":                         "retired Integration outbox capability",
		"integration_outbox":                         "retired Integration outbox owner name",
		"/business/integration-intents":              "retired Integration-named Runtime handoff route",
		"/operations/integrations":                   "retired Runtime-owned Integration operations route",
		"/management/integrations":                   "retired shell-scoped Integration route",
		"/business/notifications/web-push":           "retired shell-scoped Web Push route",
		"/integrations/webhooks":                     "retired plural Integration webhook route",
		"/integrations/web-push":                     "retired plural Integration Web Push route",
		"/integrations/google/oauth":                 "retired plural Integration OAuth route",
		"UpdateOutboxStatusByResponseRef":            "retired Provider acknowledgement persistence",
		"ListOverdueOutboxAcknowledgements":          "retired Provider acknowledgement reconciliation",
		"ack_deadline_at":                            "retired Provider acknowledgement state",
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		for fragment, reason := range forbiddenFragments {
			if strings.Contains(string(content), fragment) {
				t.Errorf("Runtime production contains %s %q in %s", reason, fragment, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	projectionFiles, err := filepath.Glob(filepath.Join(root, "domain", "capability", "projection", "capability_integration_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range projectionFiles {
		relative, _ := filepath.Rel(root, path)
		t.Errorf("Runtime-owned Integration capability projection returned: %s", filepath.ToSlash(relative))
	}
}

func TestRuntimeConsumesMetadataThroughOneSDKBinding(t *testing.T) {
	root := runtimeRoot(t)
	forbiddenTables := []string{
		"_application_schema_workflow_definitions",
		"_application_schema_automation_rule_definitions",
		"_application_schema_integration_event_mapping_requirements",
		"_application_schema_localized_texts",
	}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		relative, _ := filepath.Rel(root, path)
		for _, specification := range file.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(importPath, "github.com/domainry/domainry-metadata/") && filepath.ToSlash(relative) != "bootstrap/runtime/startup.go" {
				t.Errorf("Runtime imports Metadata implementation outside its composition root: %s -> %s", filepath.ToSlash(relative), importPath)
			}
		}
		for _, table := range forbiddenTables {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), table) {
				t.Errorf("Runtime production still owns retired Metadata table %q in %s", table, filepath.ToSlash(relative))
			}
		}
	})
	for _, relative := range []string{
		"transport/http/appschema/definition_read_handlers.go",
		"transport/http/appschema/localized_text_handlers.go",
		"transport/http/appschema/metadata/dictionaries.go",
		"domain/appschema/service/application_schema_dictionary_domain_service.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			t.Errorf("Runtime duplicate Metadata implementation returned: %s", relative)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	forbiddenOwnedPaths := []string{
		"/dictionaries/{dictionaryKey}/items",
		"/management/metadata/definitions/{resourceType}",
		"/management/metadata/definitions/{resourceType}/{resourceKey}",
		"/management/metadata/localized-texts",
		"/management/metadata/localized-texts/coverage",
		"/management/metadata/localized-texts/export",
		"/management/metadata/localized-texts/export.xlsx",
	}
	for _, relative := range []string{"domain/endpoint/model/endpoint_route_policy.go"} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range forbiddenOwnedPaths {
			if strings.Contains(string(content), strconv.Quote(path)) || strings.Contains(string(content), strconv.Quote("GET "+path)) {
				t.Errorf("Runtime still declares Metadata-owned path %q in %s", path, relative)
			}
		}
	}
}

func TestRuntimePublicPackagesDoNotImportInternalOwners(t *testing.T) {
	repositoryRoot := filepath.Dir(runtimeRoot(t))
	for _, relative := range []string{"pkg/runtimeext", "pkg/runtimehost"} {
		walkProductionGo(t, filepath.Join(repositoryRoot, filepath.FromSlash(relative)), func(path string, file *ast.File) {
			for _, specification := range file.Imports {
				importPath, err := strconv.Unquote(specification.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(importPath, "/internal/") {
					t.Errorf("public Runtime package imports internal owner %q in %s", importPath, path)
				}
			}
		})
	}
}

func TestRuntimeHasNoCatchAllDirectories(t *testing.T) {
	forbidden := map[string]bool{"common": true, "helper": true, "helpers": true, "library": true, "shared": true, "util": true, "utils": true}
	err := filepath.WalkDir(runtimeRoot(t), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != runtimeRoot(t) && forbidden[entry.Name()] {
			relative, _ := filepath.Rel(runtimeRoot(t), path)
			t.Errorf("catch-all Runtime directory is forbidden: %s", filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runtimeRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve boundary test path")
	}
	return filepath.Dir(filepath.Dir(currentFile))
}

func walkProductionGo(t *testing.T, root string, visit func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		visit(path, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
