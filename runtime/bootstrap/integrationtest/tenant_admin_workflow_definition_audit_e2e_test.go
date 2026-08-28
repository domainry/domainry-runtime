package integrationtest

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestTenantAdminPublishesWorkflowDefinitionWithAuditEvidence(t *testing.T) {
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   orderToCashSurfaceTestManifest(t),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	runtime := newIntegrationRuntime(t, cfg)
	defer runtime.CloseContext(t.Context())
	handler := runtime.Routes()

	const workflowKey = "sales_order.credit_discount_approval"
	current := loadMetadataDefinitionFixture(t, handler, "admin", "workflow", workflowKey)
	var candidate map[string]any
	if err := json.Unmarshal(current.Payload, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate["name"] = "Sales Order Credit and Discount Approval v2"
	published := publishSystemDefinitionUpdateFixture(
		t, handler, "admin", "tenant-admin-author", "tenant-admin-approver",
		"tenant-admin-workflow-definition-p7", current, candidate, "workflow.graph_v2",
	)
	if published.SchemaHash == current.SchemaHash {
		t.Fatalf("workflow definition hash did not change: %s", published.SchemaHash)
	}
	var applied map[string]any
	if err := json.Unmarshal(published.Payload, &applied); err != nil {
		t.Fatal(err)
	}
	if applied["name"] != candidate["name"] {
		t.Fatalf("published workflow name=%v want=%v", applied["name"], candidate["name"])
	}

	store := openRuntimePersistenceFixture(t, cfg)
	defer store.Close()
	var itemAuditCount, publicationAuditCount int
	if err := store.DB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM _audit_events
		 WHERE workspace_id = ? AND event = ? AND object_key = ? AND record_id = ?
		   AND actor_id = ? AND before_json <> '' AND after_json <> ''`,
		"default", "business_change_plan.item_applied", "workflow", workflowKey, "tenant-admin-author",
	).Scan(&itemAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM _audit_events
		 WHERE workspace_id = ? AND event = ? AND actor_id = ?`,
		"default", "business_change_plan.published", "tenant-admin-author",
	).Scan(&publicationAuditCount); err != nil {
		t.Fatal(err)
	}
	if itemAuditCount != 1 || publicationAuditCount != 1 {
		t.Fatalf("workflow definition audit evidence item=%d publication=%d", itemAuditCount, publicationAuditCount)
	}
}
