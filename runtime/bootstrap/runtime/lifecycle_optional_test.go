package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmodule "github.com/domainry/domainry-audit/module"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMinimalCRUDCompositionDoesNotInstallUnselectedCapabilityTables(t *testing.T) {
	cfg := config.Config{
		DatabaseDriver:       "sqlite",
		DBPath:               filepath.Join(t.TempDir(), "runtime.db"),
		UploadDir:            t.TempDir(),
		AuditExportTokenKey:  "0123456789abcdef0123456789abcdef",
		IntegrationSecretKey: "0123456789abcdef0123456789abcdef",
	}
	store, err := prepareRuntimeStore(t.Context(), cfg, persistence.RuntimeSchemaCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	metadata, err := metadatamodule.NewFactory().OpenModule(t.Context(), metadatasdk.ApplicationRef{InstallationID: "minimal"}, runtimeMetadataModuleHost{store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metadata.Close(t.Context()) })
	if err := store.BindMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	audit, err := auditmodule.NewFactory(auditmodule.Options{}).OpenModule(t.Context(), auditsdk.ApplicationRef{InstallationID: "minimal"}, runtimeauditmodule.NewHost(store, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close(t.Context()) })
	if err := store.BindAudit(audit); err != nil {
		t.Fatal(err)
	}

	assembly, err := assembleRuntimeServices(
		t.Context(), cfg,
		projectmodel.RuntimeModel{ProjectKey: "minimal", ContentHash: "minimal-model"},
		runtimeext.ProjectDefinitions{}, appschemamodel.IntegrationSchema{}, nil,
		store, nil, nil, nil, workerplatform.NormalizeDependencies(workerplatform.Dependencies{}),
		runtimeExtensionRegistries{auditBinding: audit},
	)
	if err != nil {
		t.Fatal(err)
	}
	if assembly.lifecycleBinding != nil {
		t.Fatal("minimal service assembly opened Lifecycle")
	}
	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	forbiddenPrefixes := []string{"_integration_", "_notification_", "_report_", "_data_exchange_", "_scheduler_", "_lifecycle_", "_agent_", "_monitoring_"}
	forbiddenTables := map[string]bool{
		"_workflow_executions": true, "_workflow_process_instances": true,
		"_workflow_node_instances": true,
		"_workflow_tasks":          true, "_workflow_process_events": true, "_workflow_route_steps": true,
		"_automation_runs":                 true,
		"_subject_evidence_erasure_fences": true, "_subject_evidence_erasure_receipts": true,
	}
	core := map[string]bool{
		"_audit_events": false, "_operations": false, "_artifacts": false, "_artifact_bindings": false,
		"_definitions": false, "_definition_versions": false, "_metadata_localized_texts": false,
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if _, found := core[table]; found {
			core[table] = true
		}
		if forbiddenTables[table] {
			t.Fatalf("minimal CRUD composition installed unselected Runtime table %q", table)
		}
		for _, prefix := range forbiddenPrefixes {
			if strings.HasPrefix(table, prefix) {
				t.Fatalf("minimal CRUD composition installed unselected module table %q", table)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for table, present := range core {
		if !present {
			t.Fatalf("minimal CRUD composition omitted core shared table %q", table)
		}
	}
}
