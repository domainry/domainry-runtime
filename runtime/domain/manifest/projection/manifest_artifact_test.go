package projection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRenderManifestReviewMarkdownIncludesAuditableBusinessScope(t *testing.T) {
	manifest := loadFixtureManifest(t, "crm-customer-360.json")
	markdown := RenderManifestReviewMarkdown(manifest, ReviewArtifactOptions{Title: "CRM Customer 360 Review"})
	for _, want := range []string{
		"# CRM Customer 360 Review",
		"## Object Model",
		"`customer`",
		"`opportunity`",
		"## Actions Workflows And Reports",
		"`customer.mark_risk`",
		"`customer.risk_follow_up`",
		"`crm_pipeline_health`",
		"## Seed Scenarios",
		"`customer_acme`",
		"`opportunity_acme_expansion`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("RenderManifestReviewMarkdown() missing %q\n%s", want, markdown)
		}
	}
}

func TestRenderManifestReviewMarkdownDiscoversSQLOnlyReportSources(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Reports: []reportmodel.ReportSchema{{
		Key: "order_summary",
		ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
			SQL: "SELECT COUNT(*) AS record_count FROM sales_order orders LIMIT 1",
		},
	}}}
	markdown := RenderManifestReviewMarkdown(manifest, ReviewArtifactOptions{Title: "Review"})
	if !strings.Contains(markdown, "`sales_order`") {
		t.Fatalf("SQL-owned report source missing from review artifact:\n%s", markdown)
	}
}

func loadFixtureManifest(t *testing.T, name string) manifestmodel.ManifestSchema {
	t.Helper()
	path := filepath.Join("..", "testdata", "manifests", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return manifest
}
