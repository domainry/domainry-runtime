package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportModuleOwnsProductHTTPAndApplicationBoundary(t *testing.T) {
	root := runtimeRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, implementationImport := range []string{
			"github.com/domainry/domainry-report/contract",
			"github.com/domainry/domainry-report/query/",
		} {
			if strings.Contains(string(content), implementationImport) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("Runtime production source %s imports Report implementation facade %q", filepath.ToSlash(relative), implementationImport)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{
		"transport/http/report",
		"application/report/adapter",
		"application/report/query",
		"application/report/snapshot",
		"domain/report/query",
	} {
		entries, err := os.ReadDir(filepath.Join(root, removed))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
				t.Errorf("Runtime must not restore Report-owned boundary %q (%s)", removed, entry.Name())
			}
		}
	}
	for _, relative := range []string{
		"transport/http/openapi",
		"domain/endpoint/model",
		"domain/capability/contract/capability_runtime_api_contract.go",
	} {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		check := func(file string) {
			content, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, productPath := range []string{"/report/{reportKey}", "/report-exports/{jobID}", "/report-exports/downloads/{token}"} {
				if strings.Contains(string(content), productPath) {
					rel, _ := filepath.Rel(root, file)
					t.Errorf("Runtime static product contract %s remains in %s", productPath, filepath.ToSlash(rel))
				}
			}
		}
		if !info.IsDir() {
			check(path)
			continue
		}
		err = filepath.WalkDir(path, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
				check(file)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	applications, err := os.ReadFile(filepath.Join(root, "bootstrap", "composition", "runtime_services_application_access.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{"ReportQueries", "ReportSnapshots", "ReportExports"} {
		if strings.Contains(string(applications), duplicate) {
			t.Errorf("RuntimeApplications still exposes duplicate Report service %s", duplicate)
		}
	}
	if !strings.Contains(string(applications), "Reports") || !strings.Contains(string(applications), "reportsdk.ApplicationBinding") {
		t.Fatal("RuntimeApplications must expose only the Report SDK application binding")
	}
	snapshotContract, err := os.ReadFile(filepath.Join(root, "domain", "report", "contract", "report_snapshot_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{"BeginReportSnapshot", "CompleteReportSnapshot", "FailReportSnapshot", "ReportSnapshotClaim"} {
		if strings.Contains(string(snapshotContract), duplicate) {
			t.Errorf("Runtime still republishes Report-owned snapshot mutation contract %s", duplicate)
		}
	}
	if strings.Contains(string(snapshotContract), "ReportSnapshotReader") {
		t.Fatal("Runtime must not republish Report-owned snapshot reads")
	}

	exportProvider, err := os.ReadFile(filepath.Join(root, "application", "report", "export", "report_export_data_exchange_provider.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ownerCall := range []string{"ResolveExecution", "ReadPage", "SourceVersion"} {
		if !strings.Contains(string(exportProvider), ownerCall) {
			t.Errorf("Data Exchange Report worker must use owner capability %s", ownerCall)
		}
	}
	for _, duplicate := range []string{"ReportForExport", "ExportControl func", "ReportDomainService", "ExecuteExportReport"} {
		if strings.Contains(string(exportProvider), duplicate) {
			t.Errorf("Data Exchange Report worker restored Runtime-owned definition lookup %s", duplicate)
		}
	}

	domainServicePath := filepath.Join(root, "domain", "report", "query", "report_domain_service.go")
	if _, err := os.Stat(domainServicePath); err == nil {
		t.Fatal("Runtime must not restore a second ReportDomainService execution engine")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	bindingSource, err := os.ReadFile(filepath.Join(root, "bootstrap", "composition", "report_application_binding.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bindingSource), "BindReportExports(application.Exports())") {
		t.Fatal("Runtime must bind the Report owner export resolver into the Data Exchange worker")
	}
}
